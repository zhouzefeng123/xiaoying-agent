package sys

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

type JobHandle syscall.Handle

const (
	JobObjectBasicLimitInformation    = 2
	JobObjectExtendedLimitInformation = 9
)

type JOBOBJECT_BASIC_LIMIT_INFORMATION struct {
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

type IO_COUNTERS struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type JOBOBJECT_EXTENDED_LIMIT_INFORMATION struct {
	BasicLimitInformation JOBOBJECT_BASIC_LIMIT_INFORMATION
	IoInfo                IO_COUNTERS
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

const (
	JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000
)

func CreateJobObject() (JobHandle, error) {
	// Create a job object. The name is NULL.
	ret, _, err := procCreateJobObjectW.Call(0, 0)
	if ret == 0 {
		return 0, fmt.Errorf("CreateJobObjectW failed: %v", err)
	}
	handle := JobHandle(ret)

	// Configure job object to kill processes on close.
	// This is the safe default to avoid orphan process trees that can keep
	// files/directories locked on Windows.
	info := JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	ret, _, err = procSetInformationJobObject.Call(
		uintptr(handle),
		uintptr(JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)

	if ret == 0 {
		procCloseHandle.Call(uintptr(handle))
		return 0, fmt.Errorf("SetInformationJobObject failed: %v", err)
	}

	return handle, nil
}

func AssignProcessToJobObject(job JobHandle, pid int) error {
	// Open the process with required access rights
	// PROCESS_SET_QUOTA (0x0100) | PROCESS_TERMINATE (0x0001)
	const PROCESS_SET_QUOTA = 0x0100
	const PROCESS_TERMINATE = 0x0001

	processHandle, err := syscall.OpenProcess(PROCESS_SET_QUOTA|PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess failed: %v", err)
	}
	defer syscall.CloseHandle(processHandle)

	ret, _, err := procAssignProcessToJobObject.Call(uintptr(job), uintptr(processHandle))
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject failed: %v", err)
	}
	return nil
}

func CloseJobObject(job JobHandle) error {
	ret, _, err := procCloseHandle.Call(uintptr(job))
	if ret == 0 {
		return fmt.Errorf("CloseHandle failed: %v", err)
	}
	return nil
}

func TerminateJobObject(job JobHandle, exitCode uint32) error {
	ret, _, err := procTerminateJobObject.Call(uintptr(job), uintptr(exitCode))
	if ret == 0 {
		return fmt.Errorf("TerminateJobObject failed: %v", err)
	}
	return nil
}

// KillProcessTree kills the specified process and all its children.
// This is a "one-shot" way to kill a process tree.
func KillProcessTree(pid int) error {
	job, err := CreateJobObject()
	if err != nil {
		return err
	}
	defer CloseJobObject(job)
	return AssignProcessToJobObject(job, pid)
}

