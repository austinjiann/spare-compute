//go:build windows

package localipc

import (
	"context"
	"errors"
	"net"
)

var ErrUnsupportedPlatform = errors.New("local IPC is not implemented on Windows yet")

func listen(string) (net.Listener, error) {
	return nil, ErrUnsupportedPlatform
}

func dial(context.Context, string) (net.Conn, error) {
	return nil, ErrUnsupportedPlatform
}
