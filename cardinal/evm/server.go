package evm

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sync/atomic"

	"github.com/argus-labs/world-engine/sign"
	"pkg.world.dev/world-engine/cardinal/ecs"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"pkg.world.dev/world-engine/cardinal/ecs/transaction"

	"buf.build/gen/go/argus-labs/world-engine/grpc/go/router/v1/routerv1grpc"
	"buf.build/gen/go/argus-labs/world-engine/protocolbuffers/go/router/v1"
)

var (
	_ routerv1grpc.MsgServer = &msgServerImpl{}
)

// txByID maps transaction type ID's to transaction types.
type txByID map[transaction.TypeID]transaction.ITransaction

// readByName maps read resource names to the underlying IRead
type readByName map[string]ecs.IRead

type msgServerImpl struct {
	txMap      txByID
	readMap    readByName
	world      *ecs.World
	serverOpts []grpc.ServerOption
	nextNonce  *atomic.Uint64
}

func NewServer(w *ecs.World, opts ...Option) (routerv1grpc.MsgServer, error) {
	// setup txs
	txs, err := w.ListTransactions()
	if err != nil {
		return nil, err
	}
	it := make(txByID, len(txs))
	for _, tx := range txs {
		it[tx.ID()] = tx
	}

	reads := w.ListReads()
	ir := make(readByName, len(reads))
	for _, r := range reads {
		ir[r.Name()] = r
	}

	s := &msgServerImpl{txMap: it, readMap: ir, world: w, nextNonce: &atomic.Uint64{}}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func loadCredentials(serverCertPath, serverKeyPath string) (credentials.TransportCredentials, error) {
	// Load server's certificate and private key
	sc, err := os.ReadFile(serverCertPath)
	if err != nil {
		return nil, err
	}
	sk, err := os.ReadFile(serverKeyPath)
	if err != nil {
		return nil, err
	}
	serverCert, err := tls.X509KeyPair(sc, sk)
	if err != nil {
		return nil, err
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.NoClientCert,
	}

	return credentials.NewTLS(config), nil
}

// Serve serves the application in a new go routine.
func (s *msgServerImpl) Serve(addr string) error {
	server := grpc.NewServer(s.serverOpts...)
	routerv1grpc.RegisterMsgServer(server, s)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		err = server.Serve(listener)
		if err != nil {
			panic(err)
		}
	}()
	return nil
}

// nextSig produces a signature that has a static PersonaTag and a unique nonce. srv's TxHandler wants a unique
// signature to help identify transactions, however this signature information is not readily available in evm
// messages. nextSig is a currently workaround to ensure signatures assocaited with transactions are unique.
// See https://linear.app/arguslabs/issue/CAR-133/mechanism-to-tie-evm-0x-address-to-cardinal-persona
func (s *msgServerImpl) nextSig() *sign.SignedPayload {
	return &sign.SignedPayload{
		PersonaTag: "internal-persona-tag-for-evm-server",
		Nonce:      s.nextNonce.Add(1),
	}
}

func (s *msgServerImpl) SendMessage(ctx context.Context, msg *routerv1.SendMessageRequest,
) (*routerv1.SendMessageResponse, error) {
	// first we check if we can extract the transaction associated with the id
	itx, ok := s.txMap[transaction.TypeID(msg.MessageId)]
	if !ok {
		return nil, fmt.Errorf("no transaction with ID %d is registerd in this world", msg.MessageId)
	}
	// decode the evm bytes into the transaction
	tx, err := itx.DecodeEVMBytes(msg.Message)
	if err != nil {
		return nil, err
	}
	sig := s.nextSig()
	// add transaction to the world queue
	s.world.AddTransaction(itx.ID(), tx, sig)
	return &routerv1.SendMessageResponse{}, nil
}

func (s *msgServerImpl) QueryShard(ctx context.Context, req *routerv1.QueryShardRequest) (*routerv1.QueryShardResponse, error) {
	read, ok := s.readMap[req.Resource]
	if !ok {
		return nil, fmt.Errorf("no read with name %s found", req.Resource)
	}
	ecsRequest, err := read.DecodeEVMRequest(req.Request)
	if err != nil {
		return nil, err
	}
	reply, err := read.HandleRead(s.world, ecsRequest)
	if err != nil {
		return nil, err
	}
	bz, err := read.EncodeEVMReply(reply)
	if err != nil {
		return nil, err
	}
	return &routerv1.QueryShardResponse{Response: bz}, nil
}