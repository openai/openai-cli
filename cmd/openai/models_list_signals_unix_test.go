//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// External SIGINT and raw-mode Ctrl+C use different paths. Exercise both through
// main, after observable viewer output, and retain the normal exit controls.
func TestMainModelsListSignals(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for Models signal PTY checks")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "check-models-list-signals.py"))
	require.NoError(t, err)

	type signalCase struct {
		name, stage, action string
		flags               []string
		delay               time.Duration
		code                int
	}
	var cases []signalCase
	for _, stage := range []string{"startup", "loaded"} {
		// These small offsets exercise competing shutdown paths. They are not
		// used to infer readiness; the Python observer waits for the footer.
		for repeat := range 2 {
			for _, delay := range []time.Duration{0, time.Millisecond, 10 * time.Millisecond} {
				cases = append(cases, signalCase{
					name:  fmt.Sprintf("sigint_%s/repeat_%d/delay_%s", stage, repeat, delay),
					stage: stage, action: "SIGINT", delay: delay, code: 130,
				})
			}
		}
		for _, mode := range []struct {
			name  string
			flags []string
		}{
			{"quiet", []string{"--quiet"}},
			{"json_error", []string{"--format-error", "json"}},
		} {
			cases = append(cases, signalCase{
				name:  "sigint_" + stage + "/" + mode.name,
				stage: stage, action: "SIGINT", flags: mode.flags, code: 130,
			})
		}
	}
	for _, control := range []struct {
		action string
		code   int
	}{
		{"ctrl-c", 130}, {"q", 0}, {"escape", 0}, {"p", 0},
		// Bubble Tea treats SIGTERM as a clean quit. Preserve that contract.
		{"SIGTERM", 0},
	} {
		cases = append(cases, signalCase{
			name: control.action, stage: "loaded", action: control.action, code: control.code,
		})
	}
	for _, interrupted := range []struct {
		name   string
		signal syscall.Signal
	}{{"SIGINT", syscall.SIGINT}, {"SIGTERM", syscall.SIGTERM}} {
		cases = append(cases, signalCase{
			name: interrupted.name + " while printing after viewer exit", stage: "printing", action: interrupted.name,
			flags: []string{"--format-error", "json"}, code: -int(interrupted.signal),
		})
	}

	var models []map[string]any
	for index := range 40 {
		models = append(models, map[string]any{
			"id": fmt.Sprintf("synthetic-model-%02d", index), "object": "model",
			"created": 1700000000, "owned_by": "synthetic-owner",
		})
	}
	body, err := json.Marshal(map[string]any{"object": "list", "data": models})
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responseBody, printBytes := body, 0
			if tc.stage == "printing" {
				// The viewport omits this metadata. Printing retains it and cannot
				// finish while the observer leaves the PTY output undrained.
				printingModels := make([]map[string]any, len(models))
				for index, model := range models {
					printingModels[index] = make(map[string]any, len(model)+1)
					for key, value := range model {
						printingModels[index][key] = value
					}
					printingModels[index]["metadata_padding"] = strings.Repeat("x", 64<<10)
				}
				printBytes = len(models) * (64 << 10)
				responseBody, err = json.Marshal(map[string]any{"object": "list", "data": printingModels})
				require.NoError(t, err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()
			config, err := json.Marshal(map[string]any{
				"binary": binary, "endpoint": server.URL + "/v1", "home": t.TempDir(),
				"stage": tc.stage, "action": tc.action, "flags": tc.flags,
				"delay": tc.delay.Seconds(), "expected": tc.code,
				"print_bytes": printBytes,
			})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, python, "-I", "-S", "-B", script)
			child.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "LANG=en_US.UTF-8"}
			child.Stdin = bytes.NewReader(config)
			// Let the observer close its owned PTY and child before forced cleanup.
			child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
			child.WaitDelay = 2 * time.Second
			output, err := child.CombinedOutput()
			require.NoError(t, ctx.Err(), "PTY observer timed out: %s", output)
			require.NoError(t, err, "PTY observer failed: %s", output)
			require.EqualValues(t, 1, requests.Load(), "navigation must use the loaded response")
			t.Logf("%s", bytes.TrimSpace(output))
		})
	}
}
