package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
)

func TestLocalDaemonCallErrorExplainsMismatchedIPC(t *testing.T) {
	err := localDaemonCallError(localipc.ErrMismatchedResponse)
	if !errors.Is(err, ErrDaemonProtocolMismatch) {
		t.Fatalf("error = %v; want ErrDaemonProtocolMismatch", err)
	}
	for _, want := range []string{
		"ComputeHop daemon does not match this CLI",
		"restart ComputeHop",
		"computehop doctor",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "local IPC response does not match request") {
		t.Fatalf("error leaked low-level IPC wording: %q", err)
	}
}

func TestLocalDaemonCallErrorPreservesRemoteErrors(t *testing.T) {
	remoteErr := &localipc.RemoteError{Message: "daemon rejected request"}
	if got := localDaemonCallError(remoteErr); got != remoteErr {
		t.Fatalf("error = %v; want original remote error", got)
	}
}

func TestLocalDaemonCallErrorPreservesContextErrors(t *testing.T) {
	if got := localDaemonCallError(context.DeadlineExceeded); got != context.DeadlineExceeded {
		t.Fatalf("error = %v; want deadline exceeded", got)
	}
}
