//go:build !darwin && !linux && !windows

package processes

import (
	"os"
	"os/exec"
)

type unsupportedController struct{}

func configureCommand(*exec.Cmd) {}

func newTreeController(*exec.Cmd) (treeController, error) {
	return nil, ErrUnsupportedPlatform
}

func (*unsupportedController) gracefulStop() error { return ErrUnsupportedPlatform }
func (*unsupportedController) kill() error         { return ErrUnsupportedPlatform }
func (*unsupportedController) close() error        { return nil }
func exitSignal(*os.ProcessState) string           { return "" }
func Alive(int) bool                               { return false }
func KillTree(int) error                           { return ErrUnsupportedPlatform }
func Detach(*exec.Cmd)                             {}
