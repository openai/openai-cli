//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// Native named-pipe fixtures exercise MSYS handle detection without terminal UI.
// They do not establish mintty font rendering or its actual window dimensions.
func TestMainDispatchWelcomeMSYSPipes(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	control := runWelcomeMSYSPipes(t, server.URL, "msys", "", "", []string{"--help"}, nil)
	if control.code != 0 || control.stderr != "" || !strings.HasPrefix(ansi.Strip(control.stdout), "OpenAI CLI") || !strings.Contains(ansi.Strip(control.stdout), "\nUse the OpenAI API from your terminal.") {
		t.Fatalf("MSYS explicit-help control failed: %+v", control)
	}
	control.stdout = ansi.Strip(control.stdout)
	for _, tc := range []struct {
		name, prefix, suffix, redirect string
		args, env                      []string
		banner                         bool
	}{
		{name: "msys", prefix: "msys", banner: true},
		{name: "cygwin", prefix: "cygwin", banner: true},
		{name: "msys native", prefix: "msys", suffix: "-nat", banner: true},
		{name: "cygwin native", prefix: "cygwin", suffix: "-nat", banner: true},
		{name: "cygwin legacy", prefix: "cygwin", suffix: "-cyg", banner: true},
		{name: "stdin pipe", prefix: "msys", redirect: "stdin"},
		{name: "stdout pipe", prefix: "msys", redirect: "stdout"},
		{name: "stderr pipe", prefix: "msys", redirect: "stderr"},
		{name: "ordinary named pipes", prefix: "ordinary"},
		{name: "dumb", prefix: "msys", env: []string{"TERM=dumb"}},
		{name: "CI", prefix: "msys", env: []string{"CI=true"}},
		{name: "quiet", prefix: "msys", args: []string{"--quiet"}},
		{name: "explicit help", prefix: "msys", args: []string{"--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runWelcomeMSYSPipes(t, server.URL, tc.prefix, tc.suffix, tc.redirect, tc.args, tc.env)
			want := control
			if tc.redirect == "stdout" || tc.prefix == "ordinary" {
				want.stdout = strings.TrimSuffix(want.stdout, "\n")
			}
			if tc.banner {
				header, rest, found := strings.Cut(got.stdout, "\n\n")
				plain := ansi.Strip(header)
				if !found || strings.Count(plain, "What are we making today?") != 1 ||
					!strings.Contains(plain, "OpenAI CLI") || !strings.Contains(plain, ">_") {
					t.Fatalf("MSYS welcome missing or malformed: %q", header)
				}
				for _, line := range strings.Split(plain, "\n") {
					if ansi.StringWidth(line) > 80 {
						t.Fatalf("welcome exceeds the existing unknown-width fallback: %q", line)
					}
				}
				got.stdout = rest
				_, want.stdout, _ = strings.Cut(control.stdout, "\n")
			}
			got.stdout = ansi.Strip(got.stdout)
			if got != want {
				t.Fatalf("MSYS invocation changed complete help or status: got=%+v want=%+v", got, want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("MSYS welcome made an API request")
	}
}

func runWelcomeMSYSPipes(t *testing.T, baseURL, prefix, suffix, redirect string, args, extra []string) mainDispatchResult {
	t.Helper()
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	pipe := func(stream, direction string, index int) (*os.File, *os.File) {
		name := fmt.Sprintf("%s-%x-pty%d-%s-master%s", prefix, nonce, index, direction, suffix)
		if redirect == stream {
			name = "" // An actual anonymous pipe must remain redirected.
		}
		return welcomeNamedPipe(t, name)
	}
	input, inputWriter := pipe("stdin", "from", 0)
	outputReader, output := pipe("stdout", "to", 1)
	errorReader, errorOutput := pipe("stderr", "to", 2)
	const sentinel = "welcome must not consume stdin\n"
	if _, err := io.WriteString(inputWriter, sentinel); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := append([]string{"-test.run=^TestMainDispatchWelcomeMSYSProcess$", "--", "openai"}, args...)
	process := exec.CommandContext(ctx, helper, command...)
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	process.WaitDelay = 3 * time.Second
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	process.Env = append(process.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home,
		"TEMP="+home, "TMP="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home,
		"OPENAI_BASE_URL="+baseURL, "OPENAI_CLI_WELCOME_MSYS_PROCESS=1", "GOMAXPROCS=2",
		"TERM=xterm-256color", "LANG=en_US.UTF-8", "COLORFGBG=15;0")
	process.Env = append(process.Env, extra...)
	process.Stdin, process.Stdout, process.Stderr = input, output, errorOutput
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		cancel()
		if !waited {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	})
	if err := errors.Join(output.Close(), errorOutput.Close()); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr adminConsoleCapture
	drained := make(chan error, 2)
	go func() { _, err := io.Copy(&stdout, outputReader); drained <- err }()
	go func() { _, err := io.Copy(&stderr, errorReader); drained <- err }()
	err = process.Wait()
	waited = true
	for range 2 {
		select {
		case readErr := <-drained:
			if readErr != nil {
				t.Fatal(readErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("MSYS capture did not finish after the owned process exited")
		}
	}
	if ctx.Err() != nil || stdout.overflow || stderr.overflow {
		t.Fatal("MSYS process timed out or exceeded the capture budget")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	remaining, readErr := io.ReadAll(input)
	if readErr != nil || string(remaining) != sentinel {
		t.Fatalf("welcome consumed terminal input: remaining=%q error=%v", remaining, readErr)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("MSYS welcome wrote state: entries=%v error=%v", entries, err)
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return mainDispatchResult{code, stdout.text(), stderr.text()}
}

func welcomeNamedPipe(t *testing.T, name string) (*os.File, *os.File) {
	t.Helper()
	var reader, writer *os.File
	if name == "" {
		var err error
		reader, writer, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		path, err := windows.UTF16PtrFromString(`\\.\pipe\` + name)
		if err != nil {
			t.Fatal(err)
		}
		server, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
			1, 65536, 65536, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := windows.CreateFile(path, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatal(errors.Join(err, windows.CloseHandle(server)))
		}
		// The client is already connected, so this cannot wait for another client.
		if err := windows.ConnectNamedPipe(server, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			t.Fatal(errors.Join(err, windows.CloseHandle(server), windows.CloseHandle(client)))
		}
		reader, writer = os.NewFile(uintptr(server), name), os.NewFile(uintptr(client), name)
	}
	t.Cleanup(func() {
		for _, file := range []*os.File{reader, writer} {
			if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Errorf("close owned pipe: %v", err)
			}
		}
	})
	for _, file := range []*os.File{reader, writer} {
		kind, err := windows.GetFileType(windows.Handle(file.Fd()))
		if err != nil || kind != windows.FILE_TYPE_PIPE || term.IsTerminal(file.Fd()) {
			t.Fatalf("MSYS fixture is not a pipe-backed handle: type=%d error=%v", kind, err)
		}
	}
	return reader, writer
}

func TestMainDispatchWelcomeMSYSProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WELCOME_MSYS_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing MSYS process arguments")
	}
	ownWindowsTestProcessTree(t)
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}
