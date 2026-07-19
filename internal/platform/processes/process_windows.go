//go:build windows

package processes

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const stillActiveExitCode = 259

type windowsTreeController struct {
	job windows.Handle
	pid uint32
}

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func newTreeController(command *exec.Cmd) (treeController, error) {
	jobHandle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	closeOnError := func(openErr error) (treeController, error) {
		_ = windows.CloseHandle(jobHandle)
		return nil, openErr
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return closeOnError(err)
	}
	pid := uint32(command.Process.Pid)
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		pid,
	)
	if err != nil {
		return closeOnError(err)
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(jobHandle, processHandle); err != nil {
		return closeOnError(err)
	}
	return &windowsTreeController{job: jobHandle, pid: pid}, nil
}

func (controller *windowsTreeController) gracefulStop() error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, controller.pid)
}

func (controller *windowsTreeController) kill() error {
	return windows.TerminateJobObject(controller.job, 1)
}

func (controller *windowsTreeController) close() error {
	return windows.CloseHandle(controller.job)
}

func exitSignal(*os.ProcessState) string {
	return ""
}

// Alive reports whether pid currently identifies a running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	return windows.GetExitCodeProcess(handle, &exitCode) == nil && exitCode == stillActiveExitCode
}

// KillTree is a recovery no-op because the runner-owned Job Object has
// KILL_ON_JOB_CLOSE and Windows terminates its members when the runner exits.
func KillTree(int) error {
	return nil
}

// Detach configures a runner subprocess to use its own console process group.
func Detach(command *exec.Cmd) {
	configureCommand(command)
}
