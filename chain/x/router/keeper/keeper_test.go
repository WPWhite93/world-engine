package keeper

import (
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/orm/model/ormdb"
	"cosmossdk.io/orm/model/ormtable"
	"cosmossdk.io/orm/testing/ormtest"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"gotest.tools/v3/assert"

	"github.com/argus-labs/world-engine/chain/x/router/storage"

	api "github.com/argus-labs/world-engine/chain/api/router/v1"
)

type testSuite struct {
	t     *testing.T
	db    ormdb.ModuleDB
	store api.StateStore
	k     *Keeper
	ctx   sdk.Context
}

func setupBase(t *testing.T, auth string) *testSuite {
	ts := testSuite{t: t}
	db, err := ormdb.NewModuleDB(&storage.ModuleSchema, ormdb.ModuleDBOptions{})
	assert.NilError(t, err)
	ts.db = db

	ts.store, err = api.NewStateStore(db)
	assert.NilError(t, err)

	ts.k = NewKeeper(ts.store, auth)

	memDB := dbm.NewMemDB()
	cms := store.NewCommitMultiStore(memDB, log.NewNopLogger(), metrics.NewMetrics([][]string{}))
	cms.MountStoreWithDB(storetypes.NewKVStoreKey("test"), storetypes.StoreTypeIAVL, memDB)
	assert.NilError(t, cms.LoadLatestVersion())

	ormCtx := ormtable.WrapContextDefault(ormtest.NewMemoryBackend())
	ts.ctx = sdk.NewContext(cms, types.Header{}, false, log.NewNopLogger()).WithContext(ormCtx)

	return &ts
}