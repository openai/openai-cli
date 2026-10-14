package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/urfave/cli/v3"
)

type localUtilityPartialSink struct {
	accepted bytes.Buffer
	failure  error
}

func (s *localUtilityPartialSink) Write(data []byte) (int, error) {
	n := min(len(data)/2, 19)
	_, _ = s.accepted.Write(data[:n])
	return n, s.failure
}

func TestLocalUtilityOutputFailureRecovery(t *testing.T) {
	for _, operation := range []string{"count", "inspect", "encodings", "licenses", "codex"} {
		for _, format := range []string{"text", "json"} {
			for _, fault := range []struct {
				name string
				err  error
			}{
				{"disk full", &os.PathError{Op: "write", Path: "/synthetic-private-output\x1b[31m", Err: syscall.ENOSPC}},
				{"short write", nil},
				{"canceled", context.Canceled},
				{"deadline", context.DeadlineExceeded},
			} {
				t.Run(operation+"/"+format+"/"+fault.name, func(t *testing.T) {
					sink := &localUtilityPartialSink{failure: fault.err}
					var root *cli.Command
					args := []string{"openai", "--format", format, "--format-error", format}
					if operation == "codex" {
						root = codexTestCommand(sink, codexRejectOpen)
						args = append(args, "codex", "--destination", "docs", "--open")
					} else {
						root = tokenizerTestRoot(tokenizerRejectReader{}, sink)
						args = append(args, "tokenizer", operation)
						if operation == "count" || operation == "inspect" {
							args = append(args, "--text", "hello")
						}
					}
					root.Flags = append(root.Flags, &cli.StringFlag{Name: "format-error", Value: "auto"})
					failure := root.Run(t.Context(), args)
					want := fault.err
					if want == nil {
						want = io.ErrShortWrite
					}
					if !errors.Is(failure, want) {
						t.Fatalf("lost output failure identity: %v", failure)
					}
					if sink.accepted.Len() == 0 {
						t.Fatal("the failure did not retain partial output")
					}
					var diagnostic bytes.Buffer
					if err := ShowCommandError(root, failure, &diagnostic); err != nil {
						t.Fatal(err)
					}
					message := diagnostic.String()
					if format == "json" {
						var result struct {
							Message string `json:"message"`
						}
						if err := json.Unmarshal(diagnostic.Bytes(), &result); err != nil {
							t.Fatalf("error output is not one JSON document: %v", err)
						}
						message = result.Message
					}
					for _, rejected := range []string{"synthetic-private", "\x1b", "API", "Check your arguments"} {
						if strings.Contains(message, rejected) {
							t.Errorf("unsafe or misleading output diagnostic: %q", message)
						}
					}
					if errors.Is(want, context.Canceled) {
						if strings.TrimSpace(message) != "Request canceled." {
							t.Errorf("cancellation lost priority: %q", message)
						}
						return
					}
					for _, guidance := range []string{"Could not write", "Output may be incomplete.", "Check the output file or pipe before rerunning."} {
						if !strings.Contains(message, guidance) {
							t.Errorf("missing output recovery guidance %q: %q", guidance, message)
						}
					}
				})
			}
		}
	}
}
