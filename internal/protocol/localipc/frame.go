// Package localipc implements ComputeHop's authenticated local control protocol.
package localipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

const (
	ProtocolVersion   uint32 = 1
	maximumFrameBytes        = 1 << 20
)

var (
	ErrFrameTooLarge = errors.New("local IPC frame is too large")
	ErrEmptyFrame    = errors.New("local IPC frame is empty")
)

func writeMessage(writer io.Writer, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal local IPC message: %w", err)
	}
	if len(payload) == 0 {
		return ErrEmptyFrame
	}
	if len(payload) > maximumFrameBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, len(payload), maximumFrameBytes)
	}

	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	if err := writeAll(writer, length[:]); err != nil {
		return fmt.Errorf("write local IPC frame length: %w", err)
	}
	if err := writeAll(writer, payload); err != nil {
		return fmt.Errorf("write local IPC frame payload: %w", err)
	}
	return nil
}

func writeAll(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

func readMessage(reader io.Reader, message proto.Message) error {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		return fmt.Errorf("read local IPC frame length: %w", err)
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 {
		return ErrEmptyFrame
	}
	if length > maximumFrameBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, length, maximumFrameBytes)
	}

	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("read local IPC frame payload: %w", err)
	}
	if err := proto.Unmarshal(payload, message); err != nil {
		return fmt.Errorf("unmarshal local IPC message: %w", err)
	}
	return nil
}
