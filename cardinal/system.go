package cardinal

import (
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"time"

	"github.com/rotisserie/eris"

	"pkg.world.dev/world-engine/cardinal/statsd"
	"pkg.world.dev/world-engine/cardinal/types/engine"
)

const (
	noActiveSystemName = ""
)

var _ SystemManager = &systemManager{}

// System is a user-defined function that is executed at every tick.
type System func(ctx engine.Context) error

// systemType is an internal entry used to track registered systems.
type systemType struct {
	Name string
	Fn   System
}

type SystemManager interface {
	// GetRegisteredSystems returns a slice of all registered systems' name.
	GetRegisteredSystems() []string

	// GetCurrentSystem returns the name of the currently running system.
	// If no system is currently running, it returns an empty string.
	GetCurrentSystem() string

	// These methods are intentionally made private to avoid other
	// packages from trying to modify the system manager in the middle of a tick.
	registerSystems(isInit bool, systems ...System) error
	runSystems(wCtx engine.Context) error
}

type systemManager struct {
	// Registered systems in the order that they were registered.
	// This is represented as a list as maps in Go are unordered.
	registeredSystems     []systemType
	registeredInitSystems []systemType

	// currentSystem is the name of the system that is currently running.
	currentSystem string
}

func newSystemManager() SystemManager {
	var sm SystemManager = &systemManager{
		registeredSystems:     make([]systemType, 0),
		registeredInitSystems: make([]systemType, 0),
		currentSystem:         noActiveSystemName,
	}
	return sm
}

// RegisterSystems registers multiple systems with the system manager.
// There can only be one system with a given name, which is derived from the function name.
// If isInit is true, the system will only be executed once at tick 0.
// If there is a duplicate system name, an error will be returned and none of the systems will be registered.
func (m *systemManager) registerSystems(isInit bool, systemFuncs ...System) error {
	// We create a list of systemType structs to register, and then register them in one go to ensure all or nothing.
	systemToRegister := make([]systemType, 0, len(systemFuncs))

	// Iterate throughs systemFuncs,
	// 1) Ensure that there is no duplicate system
	// 2) Create a new system entry for each one.
	for _, systemFunc := range systemFuncs {
		// Obtain the name of the system function using reflection.
		systemName := filepath.Base(runtime.FuncForPC(reflect.ValueOf(systemFunc).Pointer()).Name())

		// Check for duplicate system names within the list of systems to be registered
		if slices.ContainsFunc(
			systemToRegister,
			func(s systemType) bool { return s.Name == systemName },
		) {
			return eris.Errorf("duplicate system %q in slice", systemName)
		}

		// Checks if the system is already previously registered.
		// This will terminate the registration of all systems if any of them are already registered.
		if slices.ContainsFunc(
			slices.Concat(m.registeredSystems, m.registeredInitSystems),
			func(s systemType) bool { return s.Name == systemName },
		) {
			return eris.Errorf("System %q is already registered", systemName)
		}

		systemToRegister = append(systemToRegister, systemType{Name: systemName, Fn: systemFunc})
	}

	if isInit {
		m.registeredInitSystems = append(m.registeredInitSystems, systemToRegister...)
	} else {
		m.registeredSystems = append(m.registeredSystems, systemToRegister...)
	}

	return nil
}

// RunSystems runs all the registered system in the order that they were registered.
func (m *systemManager) runSystems(wCtx engine.Context) error {
	var systemsToRun []systemType
	if wCtx.CurrentTick() == 0 {
		systemsToRun = slices.Concat(m.registeredInitSystems, m.registeredSystems)
	} else {
		systemsToRun = m.registeredSystems
	}

	allSystemStartTime := time.Now()
	for _, sys := range systemsToRun {
		// Explicit memory aliasing
		m.currentSystem = sys.Name

		// Inject the system name into the logger
		wCtx.SetLogger(wCtx.Logger().With().Str("system", sys.Name).Logger())

		// Executes the system function that the user registered
		systemStartTime := time.Now()
		err := sys.Fn(wCtx)
		if err != nil {
			m.currentSystem = ""
			return eris.Wrapf(err, "System %s generated an error", sys.Name)
		}

		// Emit the total time it took to run `systemName`
		statsd.EmitTickStat(systemStartTime, sys.Name)
	}

	// Indicate that no system is currently running
	m.currentSystem = noActiveSystemName

	// Emit the total time it took to run all systems
	statsd.EmitTickStat(allSystemStartTime, "all_systems")

	return nil
}

func (m *systemManager) GetRegisteredSystems() []string {
	sys := slices.Concat(m.registeredInitSystems, m.registeredSystems)
	sysNames := make([]string, len(sys))
	for i, sys := range slices.Concat(m.registeredInitSystems, m.registeredSystems) {
		sysNames[i] = sys.Name
	}
	return sysNames
}

func (m *systemManager) GetCurrentSystem() string {
	return m.currentSystem
}
