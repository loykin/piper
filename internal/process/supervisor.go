package process

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/loykin/provisr/core"
	"github.com/loykin/provisr/core/history"
)

// ExitHandler receives the normalized exit status ("stopped", "failed") for a
// managed process. A registered handler is invoked at most once and may run on
// provisr's event delivery goroutine, so it must be concurrency-safe.
type ExitHandler func(status string)

type exitRegistration struct {
	pid int
	fn  ExitHandler
}

// exitSink dispatches EventStop notifications to per-process onExit handlers
// and unregisters the process from the manager after exit.
type exitSink struct {
	mu        sync.Mutex
	callbacks map[string]exitRegistration
	pending   map[string][]history.Event
}

func newExitSink() *exitSink {
	return &exitSink{
		callbacks: make(map[string]exitRegistration),
		pending:   make(map[string][]history.Event),
	}
}

func (s *exitSink) register(name string, fn ExitHandler) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.callbacks[name] = exitRegistration{fn: fn}
	delete(s.pending, name)
	s.mu.Unlock()
}

func (s *exitSink) deregister(name string) {
	s.mu.Lock()
	delete(s.callbacks, name)
	delete(s.pending, name)
	s.mu.Unlock()
}

func (s *exitSink) bindPID(name string, pid int) {
	var fn ExitHandler
	var status string

	s.mu.Lock()
	reg, ok := s.callbacks[name]
	if !ok {
		s.mu.Unlock()
		return
	}
	reg.pid = pid
	s.callbacks[name] = reg
	for _, evt := range s.pending[name] {
		if evt.Record.PID == pid {
			fn = reg.fn
			status = evt.Record.LastStatus
			delete(s.callbacks, name)
			break
		}
	}
	delete(s.pending, name)
	s.mu.Unlock()

	if fn != nil {
		fn(status)
	}
}

func (s *exitSink) Send(_ context.Context, evt history.Event) error {
	if evt.Type != history.EventStop {
		return nil
	}
	name := evt.Record.Name

	var fn ExitHandler
	s.mu.Lock()
	reg, ok := s.callbacks[name]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	if reg.pid == 0 {
		s.pending[name] = append(s.pending[name], evt)
		s.mu.Unlock()
		return nil
	}
	if reg.pid == evt.Record.PID {
		fn = reg.fn
		delete(s.callbacks, name)
	}
	s.mu.Unlock()

	if fn != nil {
		fn(evt.Record.LastStatus)
	}
	return nil
}

// ProcessSupervisor tracks named processes via provisr core.Manager.
type ProcessSupervisor struct {
	manager *core.Manager
	sink    *exitSink
}

// NewProcessSupervisor creates an empty supervisor.
func NewProcessSupervisor() *ProcessSupervisor {
	mgr := core.New()
	sink := newExitSink()
	mgr.SetHistorySinks(sink)
	return &ProcessSupervisor{manager: mgr, sink: sink}
}

// Start launches a process and tracks its lifecycle under spec.Name.
// If a process with the same name is already running it is stopped first.
func (s *ProcessSupervisor) Start(spec ProcessSpec, onExit ExitHandler) (pid int, endpoint string, err error) {
	args := ExpandArgs(spec.Command, spec.Env)

	env := make([]string, 0, len(spec.Env)+2)
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	if spec.GPUs != "" {
		env = append(env,
			"CUDA_VISIBLE_DEVICES="+spec.GPUs,
			"ROCR_VISIBLE_DEVICES="+spec.GPUs,
		)
	}

	pspec := core.Spec{
		Name:    spec.Name,
		Args:    args,
		Env:     env,
		WorkDir: spec.Dir,
		PIDFile: spec.PIDFile,
	}

	// Stop any running instance before registering the callback for the new one.
	// provisr waits for actual process exit and returns an error if it
	// cannot guarantee that the old instance is gone.
	if err := s.manager.Stop(spec.Name, 5*time.Second); err != nil && !isNotFound(err) {
		return 0, "", fmt.Errorf("stop existing process %q: %w", spec.Name, err)
	}

	// Register callback for the new start after the old process has stopped.
	s.sink.register(spec.Name, onExit)

	// Register also starts the process.
	if err := s.manager.Register(pspec); err != nil {
		s.sink.deregister(spec.Name)
		return 0, "", err
	}

	st, err := s.manager.Status(spec.Name)
	if err != nil {
		s.sink.deregister(spec.Name)
		return 0, "", err
	}
	s.sink.bindPID(spec.Name, st.PID)

	endpoint = fmt.Sprintf("http://localhost:%d", spec.Port)
	return st.PID, endpoint, nil
}

// Recover reattaches to a process recorded in spec.PIDFile. Process identity,
// liveness, and PID reuse validation are owned by provisr.
func (s *ProcessSupervisor) Recover(spec ProcessSpec, onExit ExitHandler) (pid int, running bool, err error) {
	s.sink.register(spec.Name, onExit)
	if err := s.manager.Recover(core.Spec{
		Name:    spec.Name,
		PIDFile: spec.PIDFile,
	}); err != nil {
		s.sink.deregister(spec.Name)
		return 0, false, err
	}

	st, err := s.manager.Status(spec.Name)
	if err != nil {
		s.sink.deregister(spec.Name)
		return 0, false, err
	}
	if !st.Running || st.PID <= 0 {
		s.sink.deregister(spec.Name)
		return 0, false, nil
	}
	s.sink.bindPID(spec.Name, st.PID)
	return st.PID, true, nil
}

// Stop marks a tracked process as intentionally stopped and signals it.
// Stopping a non-existent process is a no-op.
func (s *ProcessSupervisor) Stop(name string) error {
	err := s.manager.Stop(name, 5*time.Second)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not registered")
}

// KillAll stops all tracked processes and waits for their exit handlers.
func (s *ProcessSupervisor) KillAll() error {
	return s.manager.Shutdown()
}

// Status reports the current state of a tracked process.
// Returns ok=false when the process is not tracked or is stopped.
func (s *ProcessSupervisor) Status(name string) (status string, ok bool) {
	st, err := s.manager.Status(name)
	if err != nil || st.State == "stopped" || st.State == "failed" || st.State == "" {
		return "", false
	}
	return st.State, true
}

// Active returns the names of all currently running or starting processes.
func (s *ProcessSupervisor) Active() []string {
	statuses, err := s.manager.StatusAll("")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(statuses))
	for _, st := range statuses {
		if st.State != "stopped" && st.State != "failed" && st.State != "" {
			names = append(names, st.Name)
		}
	}
	return names
}
