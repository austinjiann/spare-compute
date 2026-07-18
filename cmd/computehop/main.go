package main

import (
	"context"
	"fmt"
	"io"
	"os"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
)

var version = "dev"

type caller interface {
	Call(context.Context, *localv1.Request) (*localv1.Response, error)
}

type dependencies struct {
	stdout    io.Writer
	stderr    io.Writer
	getwd     func() (string, error)
	newClient func(string) (caller, error)
}

func main() {
	command := newRootCommand(dependencies{
		stdout: os.Stdout,
		stderr: os.Stderr,
		getwd:  os.Getwd,
		newClient: func(stateDir string) (caller, error) {
			return daemonClient(stateDir)
		},
	})
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "computehop: %v\n", err)
		os.Exit(1)
	}
}

func daemonClient(stateDir string) (*localipc.Client, error) {
	var err error
	if stateDir == "" {
		stateDir, err = paths.StateDir()
		if err != nil {
			return nil, fmt.Errorf("resolve state directory: %w", err)
		}
	}
	tokenPath, err := paths.CapabilityTokenPath(stateDir)
	if err != nil {
		return nil, err
	}
	token, err := permissions.LoadCapabilityToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("load local daemon credentials: %w", err)
	}
	socketPath, err := paths.LocalSocketPath(stateDir)
	if err != nil {
		return nil, err
	}
	return localipc.NewClient(socketPath, token)
}
