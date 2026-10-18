package camera

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// Keep an owning job handle in this process. Windows closes it even after a
// forced parent termination, killing only this child and its descendants.
func protectProcess(cmd *exec.Cmd) (func(), error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	create := kernel.NewProc("CreateJobObjectW")
	set := kernel.NewProc("SetInformationJobObject")
	assign := kernel.NewProc("AssignProcessToJobObject")
	job, _, callErr := create.Call(0, 0)
	if job == 0 {
		return nil, fmt.Errorf("create child job: %v", callErr)
	}
	closeJob := func() { _ = syscall.CloseHandle(syscall.Handle(job)) }
	type basicLimits struct {
		ProcessTime, JobTime         int64
		Flags                        uint32
		MinWorkingSet, MaxWorkingSet uintptr
		ActiveProcesses              uint32
		Affinity                     uintptr
		Priority, Scheduling         uint32
	}
	type extendedLimits struct {
		Basic                                                      basicLimits
		IO                                                         [6]uint64
		ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
	}
	limits := extendedLimits{}
	limits.Basic.Flags = 0x2000
	ok, _, callErr := set.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		closeJob()
		return nil, fmt.Errorf("configure child job: %v", callErr)
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(cmd.Process.Pid))
	if err != nil {
		closeJob()
		return nil, err
	}
	defer syscall.CloseHandle(process)
	ok, _, callErr = assign.Call(job, uintptr(process))
	if ok == 0 {
		closeJob()
		return nil, fmt.Errorf("assign child job: %v", callErr)
	}
	return closeJob, nil
}
