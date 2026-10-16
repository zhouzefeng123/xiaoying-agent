//go:build !windows
package sys

import "fmt"

type JobHandle uintptr

func CreateJobObject() (JobHandle, error) {
	return 0, fmt.Errorf("not implemented on non-windows systems")
}

func AssignProcessToJobObject(job JobHandle, pid int) error {
	return nil
}

func CloseJobObject(job JobHandle) error {
	return nil
}

func TerminateJobObject(job JobHandle, exitCode uint32) error {
	return nil
}

func KillProcessTree(pid int) error {
	return nil
}

