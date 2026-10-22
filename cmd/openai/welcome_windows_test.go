//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// Exercise production main with genuine console handles and VT output disabled.
// The console is isolated and hidden; no terminal application or desktop input is used.
func TestMainDispatchWelcomeWindowsConsole(t *testing.T) {
	control := runWelcomeWindowsConsole(t, []string{"--help"}, nil)
	if !strings.HasPrefix(control, "NAME:") {
		t.Fatalf("complete help control missing: %q", control)
	}
	for _, tc := range []struct {
		name   string
		args   []string
		env    []string
		banner bool
	}{
		{"missing hints", nil, nil, true},
		{"empty hints", nil, []string{"TERM=", "WT_SESSION="}, true},
		{"dumb", nil, []string{"TERM=dumb"}, false},
		{"CI", nil, []string{"CI=true"}, false},
		{"quiet", []string{"--quiet"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runWelcomeWindowsConsole(t, tc.args, tc.env)
			if tc.banner {
				at := strings.Index(got, "NAME:")
				if at < 0 {
					t.Fatalf("welcome lost complete help: %q", got)
				}
				header := got[:at]
				if strings.Count(header, "What are we making today?") != 1 || !strings.Contains(header, "OpenAI CLI") {
					t.Fatalf("native Windows welcome missing: %q", header)
				}
				for _, char := range header {
					if char < ' ' || char > '~' {
						t.Fatalf("classic console welcome contains non-ASCII text or controls: %q", header)
					}
				}
				got = strings.TrimSpace(got[at:])
			}
			if got != control {
				t.Fatalf("existing help changed: got=%q want=%q", got, control)
			}
		})
	}
}

func runWelcomeWindowsConsole(t *testing.T, args, extra []string) string {
	t.Helper()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := append([]string{"-test.run=^TestMainDispatchWelcomeWindowsConsoleChild$", "--", "openai"}, args...)
	process := exec.CommandContext(ctx, helper, command...)
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	process.WaitDelay = 3 * time.Second
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	// Keep the welcome-only fixture offline and independent of optional update checks.
	process.Env = append(process.Env, "OPENAI_CLI_NO_UPDATE_CHECK=1", "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home,
		"TEMP="+home, "TMP="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home,
		"OPENAI_BASE_URL=not%url", "OPENAI_CLI_WELCOME_CONSOLE_CHILD=1", "GOMAXPROCS=2")
	process.Env = append(process.Env, extra...)
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	if err := process.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("native welcome process failed: %v; stdout=%q stderr=%q", err, &stdout, &stderr)
	}
	var screen string
	if err := json.Unmarshal(stdout.Bytes(), &screen); err != nil {
		t.Fatalf("native console observation missing: %v; output=%q", err, &stdout)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("welcome wrote state: entries=%v error=%v", entries, err)
	}
	return strings.TrimSpace(screen)
}

func TestMainDispatchWelcomeWindowsConsoleChild(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WELCOME_CONSOLE_CHILD") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing welcome arguments")
	}
	ownWindowsTestProcessTree(t)
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(console.Fd()) {
		t.Fatal("welcome regression requires genuine Windows console handles")
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(console.Fd()), &mode); err != nil {
		t.Fatal(err)
	}
	mode &^= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(windows.Handle(console.Fd()), mode); err != nil {
		t.Fatal(err)
	}
	for kind, handle := range map[uint32]windows.Handle{
		windows.STD_INPUT_HANDLE:  windows.Handle(input.Fd()),
		windows.STD_OUTPUT_HANDLE: windows.Handle(console.Fd()),
		windows.STD_ERROR_HANDLE:  windows.Handle(console.Fd()),
	} {
		if err := windows.SetStdHandle(kind, handle); err != nil {
			t.Fatal(err)
		}
	}
	output := os.Stdout
	os.Stdin, os.Stdout, os.Stderr = input, console, console
	os.Args = os.Args[separator+1:]
	main()
	var after uint32
	if err := windows.GetConsoleMode(windows.Handle(console.Fd()), &after); err != nil || after != mode {
		t.Fatalf("welcome changed console mode: before=%d after=%d error=%v", mode, after, err)
	}
	screen, err := readOutputConsole(console)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(output).Encode(screen); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
