package localipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestFrameRoundTrip(t *testing.T) {
	want := wrapperspb.String("hello")
	frame := &shortWriter{maximum: 2}
	if err := writeMessage(frame, want); err != nil {
		t.Fatalf("writeMessage() error = %v", err)
	}
	got := new(wrapperspb.StringValue)
	if err := readMessage(&frame.buffer, got); err != nil {
		t.Fatalf("readMessage() error = %v", err)
	}
	if got.GetValue() != want.GetValue() {
		t.Fatalf("round trip = %q, want %q", got.GetValue(), want.GetValue())
	}
}

func TestArtifactFetchUsesLongOperationDeadline(t *testing.T) {
	if !isLongOperation(&localv1.Request{Operation: &localv1.Request_FetchArtifacts{
		FetchArtifacts: &localv1.FetchArtifactsRequest{},
	}}) {
		t.Fatal("artifact fetch was not classified as a long operation")
	}
	if isLongOperation(&localv1.Request{Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}}}) {
		t.Fatal("ping was classified as a long operation")
	}
}

type shortWriter struct {
	buffer  bytes.Buffer
	maximum int
}

func (writer *shortWriter) Write(contents []byte) (int, error) {
	if len(contents) > writer.maximum {
		contents = contents[:writer.maximum]
	}
	return writer.buffer.Write(contents)
}

func TestReadMessageRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	var frame bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], maximumFrameBytes+1)
	frame.Write(length[:])

	err := readMessage(&frame, new(wrapperspb.StringValue))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("readMessage() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadMessageRejectsEmptyFrame(t *testing.T) {
	var frame bytes.Buffer
	frame.Write(make([]byte, 4))
	if err := readMessage(&frame, new(wrapperspb.StringValue)); !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("readMessage() error = %v, want ErrEmptyFrame", err)
	}
}
