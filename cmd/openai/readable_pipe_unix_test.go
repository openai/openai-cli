//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainReadableClosedStdoutPipe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("unexpected synthetic request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": strings.Repeat("x", 8000), "object": "model"}}})
	}))
	defer server.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", "auto", "text", "json"} {
		t.Run("format="+format, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
			defer writer.Close()
			args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}
			if format != "" {
				args = append(args, "--format", format)
			}
			args = append(args, "models", "list")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-pipe-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
			var stderr bytes.Buffer
			command.Stdout, command.Stderr = writer, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("closed stdout must finish cleanly: %v; stderr=%q", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected diagnostic: %q", stderr.String())
			}
		})
	}
}
