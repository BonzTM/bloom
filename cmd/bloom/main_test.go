package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunCommandDoesNotReadBootstrapPasswordForNonAdminPaths(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "serve"},
		{name: "migrate", args: []string{"-migrate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BLOOM_SECRET_KEY", "")
			consulted := 0
			err := runCommand(context.Background(), tt.args, func(io.Writer) ([]byte, error) {
				consulted++
				return nil, errors.New("unexpected password read")
			})
			if err == nil {
				t.Fatal("runCommand succeeded with missing service secret")
			}
			if consulted != 0 {
				t.Fatalf("bootstrap password source consulted %d times, want 0", consulted)
			}
		})
	}
}

func TestPasswordPromptUsesConsoleNotAuditStream(t *testing.T) {
	var console, audit strings.Builder
	audit.WriteString(`{"log_type":"audit","action":"account.create_admin"}` + "\n")
	password, err := promptForPassword(&console, func() ([]byte, error) {
		return []byte("secret"), nil
	})
	if err != nil || string(password) != "secret" {
		t.Fatalf("promptForPassword = %q, %v", password, err)
	}
	if console.String() != "Password: \n" {
		t.Fatalf("console = %q, want prompt", console.String())
	}
	var record map[string]any
	decoder := json.NewDecoder(strings.NewReader(audit.String()))
	if err := decoder.Decode(&record); err != nil || record["log_type"] != "audit" {
		t.Fatalf("audit stream = %q, want JSON-only audit record: %v", audit.String(), err)
	}
	if strings.Contains(audit.String(), "Password:") {
		t.Fatal("audit stream contains password prompt")
	}
}

type failNthWriter struct {
	writes int
	failAt int
	err    error
}

func (w *failNthWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestPasswordPromptReturnsNewlineWriteErrorAndPreservesReadError(t *testing.T) {
	readErr := errors.New("terminal read failed")
	writeErr := errors.New("console write failed")
	console := &failNthWriter{failAt: 2, err: writeErr}
	_, err := promptForPassword(console, func() ([]byte, error) { return nil, readErr })
	if !errors.Is(err, readErr) || !errors.Is(err, writeErr) {
		t.Fatalf("promptForPassword error = %v, want read and newline write errors", err)
	}
}
