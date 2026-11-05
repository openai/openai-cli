//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainResourceListActivationTerminal(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for private PTY list checks")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "check-resource-list-activation.py"))
	require.NoError(t, err)
	type terminalCase struct {
		name, mode, action string
		route              resourceListRoute
		flags              []string
		code, calls        int
	}
	var cases []terminalCase
	for _, route := range resourceListRoutes {
		cases = append(cases, terminalCase{name: route.name, mode: "normal", action: "paging", route: route, calls: 2})
	}
	files := resourceListRoutes[0]
	cases = append(cases, terminalCase{name: "files-ignored-legacy-keys", mode: "normal", action: "paging-legacy-keys", route: files, calls: 2})
	for _, mode := range []string{"complete", "empty", "unfamiliar"} {
		cases = append(cases, terminalCase{name: mode, mode: mode, action: "finish", route: files, calls: 1})
	}
	cases = append(cases,
		terminalCase{name: "max-items-one", mode: "normal", action: "one", route: files, flags: []string{"--max-items", "1"}, calls: 1},
		terminalCase{name: "zero-error", mode: "error-first", action: "error", route: files, flags: []string{"--max-items", "0", "--format-error", "json"}, calls: 1, code: 1},
		terminalCase{name: "later-error", mode: "error-next", action: "error-next", route: files, flags: []string{"--format-error", "json"}, calls: 2, code: 1},
		terminalCase{name: "auto-quiet", mode: "complete", action: "finish", route: files, flags: []string{"--format", "auto", "--quiet"}, calls: 1},
	)
	for _, stage := range []string{"blocked-first", "blocked-next"} {
		for _, action := range []struct {
			name string
			code int
		}{{"q", 0}, {"ctrl-c", 130}, {"SIGINT", 130}, {"SIGTERM", 143}} {
			calls := 1
			if stage == "blocked-next" {
				calls = 2
			}
			var flags []string
			if action.code != 0 {
				flags = []string{"--format-error", "json"}
			}
			cases = append(cases, terminalCase{name: stage + "/" + action.name, mode: stage, action: action.name,
				route: files, flags: flags, code: action.code, calls: calls})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResourceListFixture(t, tc.route, tc.mode)
			config, err := json.Marshal(map[string]any{
				"binary": binary, "endpoint": fixture.server.URL, "home": t.TempDir(),
				"args": resourceListArgs(tc.route, tc.flags...), "headers": tc.route.headers,
				"mode": tc.mode, "action": tc.action, "expected": tc.code,
			})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, python, "-I", "-S", "-B", script)
			child.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "LANG=en_US.UTF-8"}
			child.Stdin = bytes.NewReader(config)
			child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
			child.WaitDelay = 2 * time.Second
			output, err := child.CombinedOutput()
			require.NoError(t, ctx.Err(), "PTY observer timed out: %s", output)
			require.NoError(t, err, "PTY observer failed: %s", output)
			fixture.assertCursors(t, tc.route, tc.calls)
			t.Logf("%s", bytes.TrimSpace(output))
		})
	}
}
