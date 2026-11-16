//go:build !windows

package scripts_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run independently of scripts/test in CI: the launcher must not hide a failed
// Go process, including failures from these tests themselves.
func TestTestRunner(t *testing.T) {
	launcher, err := os.ReadFile("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, skip, nativeStatus, windowsStatus string
		wantStatus                              int
		wantWindows                             bool
	}{
		{"default success", "", "0", "0", 0, true},
		{"CI success", "1", "0", "0", 0, false},
		{"native failure", "", "23", "0", 23, false},
		{"CI native failure", "1", "23", "0", 23, false},
		{"Windows compilation failure", "", "0", "29", 29, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"scripts", "bin", "home", "outside"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			launcherPath := filepath.Join(root, "scripts", "test")
			if err := os.WriteFile(launcherPath, launcher, 0o755); err != nil {
				t.Fatal(err)
			}
			fakeGo := `#!/bin/bash
set -euo pipefail
printf '%s\0' "${GOOS-}" "${GOARCH-}" "$PWD" "$@" >> "$TEST_RUNNER_LOG"
if [[ "${GOOS-}" == windows ]]; then
  exit "$TEST_RUNNER_WINDOWS_STATUS"
fi
exit "$TEST_RUNNER_NATIVE_STATUS"
`
			binDir := filepath.Join(root, "bin")
			if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
				t.Fatal(err)
			}
			unexpectedPath := filepath.Join(root, "unexpected.log")
			for _, name := range []string{"bin/curl", "bin/lsof", "scripts/mock"} {
				guard := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$TEST_RUNNER_UNEXPECTED\"\nexit 97\n"
				if err := os.WriteFile(filepath.Join(root, name), []byte(guard), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			logPath := filepath.Join(root, "go.log")
			arguments := []string{"-p", "1", "-run", "^TestExample/name with spaces$", "-count=1"}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", append([]string{launcherPath}, arguments...)...)
			command.WaitDelay = time.Second
			command.Dir = filepath.Join(root, "outside")
			command.Env = []string{
				"PATH=" + binDir + ":/usr/bin:/bin",
				"HOME=" + filepath.Join(root, "home"),
				// The fake Go process makes no requests. This bypasses mock setup.
				"TEST_API_BASE_URL=http://127.0.0.1:1",
				"TEST_RUNNER_LOG=" + logPath,
				"TEST_RUNNER_UNEXPECTED=" + unexpectedPath,
				"TEST_RUNNER_NATIVE_STATUS=" + test.nativeStatus,
				"TEST_RUNNER_WINDOWS_STATUS=" + test.windowsStatus,
			}
			if test.skip != "" {
				command.Env = append(command.Env, "SKIP_WINDOWS_TEST_COMPILE="+test.skip)
			}
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("test launcher timed out: %v; output: %s", ctx.Err(), output)
			}
			status := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("start test launcher: %v", err)
				}
				status = exit.ExitCode()
			}
			if status != test.wantStatus {
				t.Errorf("launcher exit status = %d, want %d; output: %s", status, test.wantStatus, output)
			}
			if calls, err := os.ReadFile(unexpectedPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("unexpected mock or network command: %q; read error: %v", calls, err)
			}
			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			// NUL separators preserve each argument boundary, including spaces.
			want := append([]string{"", "", root, "test", "./..."}, arguments...)
			if test.wantWindows {
				want = append(want, "windows", "amd64", root, "test", "-exec=true", "./...")
				want = append(want, arguments...)
			}
			if string(calls) != strings.Join(want, "\x00")+"\x00" {
				t.Errorf("Go invocations = %q, want %q", calls, want)
			}
		})
	}
}
