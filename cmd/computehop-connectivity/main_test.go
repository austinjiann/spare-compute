package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunVersionDoesNotStartServer(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != version+"\n" || stderr.Len() != 0 {
		t.Fatalf("stdout = %q; stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunStartsAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if err := run(ctx, []string{"--listen", "127.0.0.1:0"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "listening on 127.0.0.1:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunRejectsInvalidLimitsAndArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{"--max-routes", "-1"},
		{"--max-payload-bytes", "0"},
		{"unexpected"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), arguments, &stdout, &stderr); err == nil {
			t.Fatalf("run(%v) accepted invalid options", arguments)
		}
	}
}
