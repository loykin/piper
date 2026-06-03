package workload

import (
	"fmt"
	"os/exec"
	"sync"
)

// ExitHandler receives the normalized exit status for a managed process.
type ExitHandler func(status string)

type processRecord struct {
	pid        int
	stopped    bool
	generation int64
	onExit     ExitHandler
}

// ProcessSupervisor tracks named processes and normalizes their shutdown path.
type ProcessSupervisor struct {
	mu        sync.Mutex
	processes map[string]*processRecord
	nextGen   int64
	wg        sync.WaitGroup
}

// NewProcessSupervisor creates an empty supervisor.
func NewProcessSupervisor() *ProcessSupervisor {
	return &ProcessSupervisor{
		processes: make(map[string]*processRecord),
	}
}

// Start launches a process and tracks its lifecycle under name.
func (s *ProcessSupervisor) Start(spec ProcessSpec, onExit ExitHandler) (pid int, endpoint string, err error) {
	pid, endpoint, cmd, err := StartProcess(spec)
	if err != nil {
		return 0, "", err
	}

	s.mu.Lock()
	s.nextGen++
	gen := s.nextGen
	s.processes[spec.Name] = &processRecord{
		pid:        pid,
		generation: gen,
		onExit:     onExit,
	}
	s.wg.Add(1)
	s.mu.Unlock()

	go s.watch(spec.Name, gen, cmd)
	return pid, endpoint, nil
}

// Stop marks a tracked process as intentionally stopped and best-effort kills it.
func (s *ProcessSupervisor) Stop(name string) error {
	pid, ok := s.markStopped(name)
	if !ok {
		return nil
	}
	KillPID(pid)
	return nil
}

// KillAll stops all tracked processes and waits for their exit handlers.
func (s *ProcessSupervisor) KillAll() error {
	pids := s.markAllStopped()
	for _, pid := range pids {
		KillPID(pid)
	}
	s.wg.Wait()
	return nil
}

func (s *ProcessSupervisor) markStopped(name string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.processes[name]
	if !ok {
		return 0, false
	}
	rec.stopped = true
	return rec.pid, true
}

func (s *ProcessSupervisor) markAllStopped() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pids := make([]int, 0, len(s.processes))
	for _, rec := range s.processes {
		rec.stopped = true
		pids = append(pids, rec.pid)
	}
	return pids
}

func (s *ProcessSupervisor) watch(name string, gen int64, cmd *exec.Cmd) {
	defer s.wg.Done()

	err := cmd.Wait()
	status := "stopped"
	if err != nil {
		status = "failed"
	}

	var onExit ExitHandler
	s.mu.Lock()
	rec, ok := s.processes[name]
	if ok && rec.generation == gen {
		if rec.stopped {
			status = "stopped"
		}
		onExit = rec.onExit
		delete(s.processes, name)
	}
	s.mu.Unlock()

	if onExit != nil {
		onExit(status)
	}
}

// Active returns the names of all tracked processes.
func (s *ProcessSupervisor) Active() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := make([]string, 0, len(s.processes))
	for name := range s.processes {
		active = append(active, name)
	}
	return active
}

// String is useful in debugging and tests.
func (s *ProcessSupervisor) String() string {
	return fmt.Sprintf("ProcessSupervisor(active=%d)", len(s.Active()))
}
