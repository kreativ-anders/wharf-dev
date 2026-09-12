package proctree

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")

	// NtResumeProcess resumes every thread of a process given only its handle;
	// os/exec closes the main thread's handle, so ResumeThread is not an option.
	procNtResumeProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
)

const (
	createSuspended              = 0x00000004
	jobInfoClassExtendedLimit    = 9 // JobObjectExtendedLimitInformation
	jobObjectLimitKillOnJobClose = 0x00002000
	processTerminate             = 0x0001
	processSetQuota              = 0x0100
	processSuspendResume         = 0x0800
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// Tree is a started process and everything it starts.
type Tree struct {
	mu  sync.Mutex
	job syscall.Handle // zero once released
}

// Start starts cmd inside a new job object. The process is created suspended
// and resumed only once it is in the job, so not even a worker started in its
// first instant can escape.
func Start(cmd *exec.Cmd) (*Tree, error) {
	job, err := newJob()
	if err != nil {
		return nil, fmt.Errorf("create a Windows job object: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	if err := cmd.Start(); err != nil {
		syscall.CloseHandle(job)
		return nil, err
	}
	if err := adopt(job, cmd.Process.Pid); err != nil {
		// Still suspended, so it has started nothing that could be left behind.
		cmd.Process.Kill()
		cmd.Wait()
		syscall.CloseHandle(job)
		return nil, fmt.Errorf("place the process in a Windows job object, without which its workers could outlive it: %w", err)
	}
	return &Tree{job: job}, nil
}

// newJob creates an unnamed job whose processes are killed when its last
// handle closes. The handle is not inheritable, so the daemon holds the only
// one and its exit — clean, crashed or killed — ends the job.
func newJob() (syscall.Handle, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	var info jobObjectExtendedLimitInformation
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, err = procSetInformationJobObject.Call(uintptr(job), jobInfoClassExtendedLimit,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func adopt(job syscall.Handle, pid int) error {
	h, err := syscall.OpenProcess(processTerminate|processSetQuota|processSuspendResume, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(job), uintptr(h)); r == 0 {
		return err
	}
	if status, _, _ := procNtResumeProcess.Call(uintptr(h)); status != 0 {
		return fmt.Errorf("NtResumeProcess: status 0x%x", status)
	}
	return nil
}

// Alive reports whether a process is still running. An exited process stays
// openable while anyone holds a handle to it — the flutter tool that launched
// the app, a debugger — so it is asked whether it has exited, not merely
// whether it can be opened.
func Alive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// Denied means it exists but is not ours to open.
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	ev, _ := syscall.WaitForSingleObject(h, 0)
	return ev == syscall.WAIT_TIMEOUT
}

// Kill terminates every process in the tree at once.
func (t *Tree) Kill() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == 0 {
		return nil
	}
	if r, _, err := procTerminateJobObject.Call(uintptr(t.job), 1); r == 0 {
		return err
	}
	return nil
}

// Release is called once the root process has exited. Closing the job kills
// whatever it left behind: a master that crashed must not leave workers
// holding its port.
func (t *Tree) Release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		syscall.CloseHandle(t.job)
		t.job = 0
	}
}
