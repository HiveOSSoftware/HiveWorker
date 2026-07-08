package process

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	hiveruntime "hivepanel-worker/internal/runtime"

	ps "github.com/shirou/gopsutil/v4/process"
)

type ProcessRuntime struct {
	mutex     sync.Mutex
	processes map[string]*RunningProcess
}

type RunningProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt time.Time
}

func New() *ProcessRuntime {
	return &ProcessRuntime{
		processes: map[string]*RunningProcess{},
	}
}

func (r *ProcessRuntime) Start(cell hiveruntime.RuntimeCell, onOutput func(line string), onExit func()) error {
	r.mutex.Lock()

	if _, exists := r.processes[cell.ID]; exists {
		r.mutex.Unlock()
		return errors.New("cell already running")
	}

	cmd := shellCommand(cell.Command)
	cmd.Dir = cell.InstanceDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.mutex.Unlock()
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.mutex.Unlock()
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.mutex.Unlock()
		return err
	}

	if err := cmd.Start(); err != nil {
		r.mutex.Unlock()
		return err
	}

	r.processes[cell.ID] = &RunningProcess{
		cmd:       cmd,
		stdin:     stdin,
		startedAt: time.Now(),
	}

	r.mutex.Unlock()

	go capture(stdout, onOutput)
	go capture(stderr, onOutput)

	go func() {
		_ = cmd.Wait()

		r.mutex.Lock()
		delete(r.processes, cell.ID)
		r.mutex.Unlock()

		onExit()
	}()

	return nil
}

func (r *ProcessRuntime) Stop(id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	running, exists := r.processes[id]
	if !exists {
		return errors.New("cell is not running")
	}

	return running.cmd.Process.Kill()
}

func (r *ProcessRuntime) SendCommand(id string, command string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	running, exists := r.processes[id]
	if !exists {
		return errors.New("cell is not running")
	}

	_, err := io.WriteString(running.stdin, command+"\n")
	return err
}

func (r *ProcessRuntime) Stats(id string) (*hiveruntime.Stats, error) {
	r.mutex.Lock()
	running, exists := r.processes[id]
	if !exists {
		r.mutex.Unlock()
		return &hiveruntime.Stats{}, nil
	}

	pid := running.cmd.Process.Pid
	r.mutex.Unlock()

	stats := &hiveruntime.Stats{
		PID: pid,
	}

	proc, err := ps.NewProcess(int32(pid))
	if err == nil {
		cpu, err := proc.CPUPercent()
		if err == nil {
			stats.CPU = cpu
		}

		mem, err := proc.MemoryInfo()
		if err == nil && mem != nil {
			stats.MemoryMB = float64(mem.RSS) / 1024 / 1024
		}
	}

	return stats, nil
}

func (r *ProcessRuntime) IsRunning(id string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	_, exists := r.processes[id]
	return exists
}

func (r *ProcessRuntime) StartedAt(id string) *time.Time {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	running, exists := r.processes[id]
	if !exists {
		return nil
	}

	startedAt := running.startedAt
	return &startedAt
}

func capture(pipe io.ReadCloser, onOutput func(line string)) {
	scanner := bufio.NewScanner(pipe)

	for scanner.Scan() {
		onOutput(scanner.Text())
	}
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-Command", command)
	}

	return exec.Command("bash", "-lc", command)
}
