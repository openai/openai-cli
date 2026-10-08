//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainSummaryKeepsStderrPipeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"file_synthetic","object":"file","created_at":17}],"has_more":false}`)
	}))
	defer server.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"summary diagnostic", nil, 1},
		{"quiet suppresses optional diagnostic", []string{"--quiet"}, 0},
		{"machine output has no summary", []string{"--format", "jsonl"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, tc.args...)
			child := exec.CommandContext(ctx, binary, append(args, "files", "list")...)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-output-test", "FORCE_COLOR=0"}
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, writer
			err = child.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if ctx.Err() != nil || code != tc.code || !strings.Contains(output.String(), "file_synthetic") || strings.Contains(output.String(), "Summary;") {
				t.Fatalf("stderr failure changed data/status: exit=%d stdout=%q context=%v", code, output.String(), ctx.Err())
			}
		})
	}
}
