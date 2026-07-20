//go:build darwin || linux

package processes

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type unixTreeController struct {
	processGroupID int
}

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func newTreeController(command *exec.Cmd) (treeController, error) {
	return &unixTreeController{processGroupID: command.Process.Pid}, nil
}

func (controller *unixTreeController) gracefulStop() error {
	return ignoreMissingProcess(unix.Kill(-controller.processGroupID, unix.SIGTERM))
}

func (controller *unixTreeController) kill() error {
	return ignoreMissingProcess(unix.Kill(-controller.processGroupID, unix.SIGKILL))
}

func (controller *unixTreeController) close() error {
	return nil
}

func exitSignal(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}

// Alive reports whether pid currently identifies a process the caller can see.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

// KillTree force-stops a previously recorded native process group.
func KillTree(rootPID int) error {
	if rootPID <= 0 {
		return nil
	}
	return ignoreMissingProcess(unix.Kill(-rootPID, unix.SIGKILL))
}

// Detach configures a runner subprocess to survive daemon process-group signals.
func Detach(command *exec.Cmd) {
	configureCommand(command)
}

func ignoreMissingProcess(err error) error {
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
