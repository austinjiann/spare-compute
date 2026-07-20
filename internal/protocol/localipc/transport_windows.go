//go:build windows

package localipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(path string) (net.Listener, error) {
	sddl, err := currentUserPipeSDDL()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(pipeName(path), &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    maximumFrameBytes + 4,
		OutputBufferSize:   maximumFrameBytes + 4,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on user-scoped local IPC pipe: %w", err)
	}
	return listener, nil
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(path))
}

func pipeName(path string) string {
	canonical := strings.ToLower(filepath.Clean(path))
	digest := sha256.Sum256([]byte(canonical))
	return `\\.\pipe\computehop-` + hex.EncodeToString(digest[:16])
}

func currentUserPipeSDDL() (string, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("resolve current Windows user for local IPC: %w", err)
	}
	if user == nil || user.User.Sid == nil {
		return "", fmt.Errorf("resolve current Windows user for local IPC: missing SID")
	}
	// Protected DACL: the current user and LocalSystem have full access. The
	// per-install capability token remains a second authentication layer.
	return "D:P(A;;GA;;;" + user.User.Sid.String() + ")(A;;GA;;;SY)", nil
}
