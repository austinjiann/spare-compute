package main

import (
	"context"
	"errors"
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

type localDaemonCaller struct {
	client *localipc.Client
}

func (value localDaemonCaller) Call(ctx context.Context, request *localv1.Request) (*localv1.Response, error) {
	response, err := value.client.Call(ctx, request)
	if err == nil {
		return response, nil
	}
	return nil, localDaemonCallError(err)
}

func localDaemonCallError(err error) error {
	var remoteError *localipc.RemoteError
	if errors.As(err, &remoteError) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, localipc.ErrMismatchedResponse) {
		return fmt.Errorf(
			"%w; restart ComputeHop or reinstall from this checkout, then run 'computehop doctor'",
			ErrDaemonProtocolMismatch,
		)
	}
	return fmt.Errorf(
		"%w; run 'computehop doctor' for setup help: %v",
		ErrDaemonNotRunning,
		err,
	)
}

func main() {
	command := newRootCommand(dependencies{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		getwd:     os.Getwd,
		newClient: daemonClient,
	})
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "computehop: %v\n", err)
		os.Exit(1)
	}
}

func daemonClient(stateDir string) (caller, error) {
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
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"%w; run 'computehop doctor' for setup help",
				ErrDaemonNotRunning,
			)
		}
		return nil, fmt.Errorf("load local daemon credentials: %w", err)
	}
	socketPath, err := paths.LocalSocketPath(stateDir)
	if err != nil {
		return nil, err
	}
	client, err := localipc.NewClient(socketPath, token)
	if err != nil {
		return nil, err
	}
	return localDaemonCaller{client: client}, nil
}
