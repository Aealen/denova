package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"denova/config"
	"denova/internal/agents/conversationconfig"
	"denova/internal/agents/external"
	"denova/internal/agents/external/codex"
	"denova/internal/hostruntime"
)

var (
	ErrEngineNotFound         = errors.New("agent runtime is not registered")
	ErrEngineNotInstalled     = errors.New("agent runtime is not installed")
	ErrEngineNotReady         = errors.New("agent runtime is not ready")
	ErrEngineModelUnavailable = errors.New("agent runtime model or effort is unavailable")
)

// Engines owns host-local connections and their active-use leases. Creating or
// listing it has no filesystem, process, account, or network side effects.
// Native execution never calls Acquire and retains its existing runtime.
type Engines struct {
	// Admissions may prepare concurrently. A selection change excludes their
	// final accept step across Writing and AgentChat, which share one journal.
	admission  sync.RWMutex
	entries    map[config.RuntimeID]*engineEntry
	Operations external.Service
}

type engineEntry struct {
	mu         sync.Mutex
	id         config.RuntimeID
	factory    func(context.Context) (external.Connection, error)
	connection external.Connection
	state      external.ConnectionState
	users      int
	closed     bool
}

func NewEngines() *Engines {
	return &Engines{entries: map[config.RuntimeID]*engineEntry{config.RuntimeCodex: {
		id: config.RuntimeCodex, state: external.ConnectionState{Status: "unchecked"}, factory: connectCodex,
	}}}
}

func connectCodex(ctx context.Context) (external.Connection, error) {
	executable := hostruntime.DiscoverCodex(os.Environ())
	if executable == "" {
		return nil, ErrEngineNotInstalled
	}
	home, err := hostruntime.CodexHome(os.Environ())
	if err != nil {
		return nil, err
	}
	return codex.Connect(ctx, codex.ProcessOptions{Executable: executable, Home: home})
}

func (engines *Engines) Catalog() []EngineDescriptor {
	items := []EngineDescriptor{engineDescriptor(config.RuntimeNative)}
	for _, id := range []config.RuntimeID{config.RuntimeCodex} {
		entry := engines.entries[id]
		entry.mu.Lock()
		items = append(items, entry.descriptor())
		entry.mu.Unlock()
	}
	return items
}

func (entry *engineEntry) descriptor() EngineDescriptor {
	item := engineDescriptor(entry.id)
	state := entry.state
	if entry.connection != nil {
		state = entry.connection.Status()
	}
	item.Status, item.ReasonKey = state.Status, state.ReasonKey
	return item
}

func (engines *Engines) entry(id config.RuntimeID) (*engineEntry, error) {
	entry := engines.entries[id]
	if entry == nil {
		if id != config.RuntimeNative {
			return nil, ErrEngineNotFound
		}
		return nil, conversationconfig.ErrRuntimeCapabilityUnsupported
	}
	return entry, nil
}

func (entry *engineEntry) connect(ctx context.Context) error {
	if entry.closed {
		return ErrEngineNotReady
	}
	if entry.connection != nil && entry.connection.Status().Status != "unavailable" {
		return nil
	}
	if entry.users != 0 {
		return ErrOperationActive
	}
	if entry.connection != nil {
		_ = entry.connection.Close()
		entry.connection = nil
	}
	connection, err := entry.factory(ctx)
	if err != nil {
		entry.state = external.ConnectionState{Status: "unavailable", ReasonKey: "agentRuntime.connectionFailed"}
		if errors.Is(err, ErrEngineNotInstalled) {
			entry.state = external.ConnectionState{Status: "not_installed", ReasonKey: "agentRuntime.notInstalled"}
		}
		if errors.Is(err, codex.ErrVersionUnsupported) {
			entry.state = external.ConnectionState{Status: "incompatible", ReasonKey: "agentRuntime.incompatibleVersion"}
		}
		return err
	}
	entry.connection = connection
	return nil
}

func (engines *Engines) Check(ctx context.Context, id config.RuntimeID) (EngineDescriptor, error) {
	if id == config.RuntimeNative {
		return engineDescriptor(id), nil
	}
	entry, err := engines.entry(id)
	if err != nil {
		return EngineDescriptor{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// CLI login and configuration changes happen outside this process. An
	// explicit idle check reloads them; accepted operations retain their lease.
	if entry.connection != nil && entry.users == 0 {
		_ = entry.connection.Close()
		entry.connection = nil
	}
	if err := entry.connect(ctx); err != nil {
		return entry.descriptor(), nil
	}
	state, err := entry.connection.Check(ctx)
	if err != nil {
		state = external.ConnectionState{Status: "unavailable", ReasonKey: "agentRuntime.connectionFailed"}
	}
	entry.state = state
	item := engineDescriptor(id)
	item.Status, item.ReasonKey = state.Status, state.ReasonKey
	return item, nil
}

func (engines *Engines) Models(ctx context.Context, id config.RuntimeID) (external.Models, error) {
	entry, err := engines.entry(id)
	if err != nil {
		return external.Models{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := entry.connect(ctx); err != nil {
		return external.Models{}, err
	}
	state, err := entry.connection.Check(ctx)
	if err != nil {
		return external.Models{}, err
	}
	if state.Status != "ready" {
		return external.Models{}, ErrEngineNotReady
	}
	return entry.connection.Models(ctx)
}

// Acquire pins the connection from preflight through durable settlement. The
// caller releases on acceptance failure or after Wait, including cancellation.
func (engines *Engines) Acquire(ctx context.Context, selection config.RuntimeSelection) (external.Adapter, func(), error) {
	entry, err := engines.entry(selection.Kind)
	if err != nil {
		return nil, nil, err
	}
	if err := selection.Validate(config.AgentKindGeneral); err != nil {
		return nil, nil, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := entry.connect(ctx); err != nil {
		return nil, nil, err
	}
	state, err := entry.connection.Check(ctx)
	if err != nil {
		return nil, nil, err
	}
	if state.Status != "ready" {
		return nil, nil, ErrEngineNotReady
	}
	models, err := entry.connection.Models(ctx)
	if err != nil {
		return nil, nil, err
	}
	valid := false
	for _, model := range models.Items {
		if model.ID == selection.Codex.Model && (selection.Codex.Effort == "" || slices.Contains(model.Efforts, selection.Codex.Effort)) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, nil, ErrEngineModelUnavailable
	}
	entry.users++
	var once sync.Once
	return entry.connection, func() { once.Do(func() { entry.mu.Lock(); entry.users--; entry.mu.Unlock() }) }, nil
}

func (engines *Engines) Close() error {
	var failures []error
	for _, entry := range engines.entries {
		entry.mu.Lock()
		entry.closed = true
		if entry.connection != nil {
			if err := entry.connection.Close(); err != nil {
				failures = append(failures, fmt.Errorf("close %s: %w", entry.id, err))
			}
		}
		entry.mu.Unlock()
	}
	return errors.Join(failures...)
}
