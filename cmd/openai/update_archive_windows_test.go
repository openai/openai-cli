//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// Call this only after ordinary enrollment under the disposable standard account.
func verifyWindowsOrdinaryUpdateAdmission(t *testing.T, managed string) {
	t.Helper()
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.URL.Path != "/v1/models/fixture-model" {
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture-model","object":"model","created":1,"owned_by":"fixture"}`)
	}))
	defer api.Close()
	attempts := make(chan string, 8)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case attempts <- req.Method + " " + req.Host:
		default:
		}
		http.Error(w, "offline fixture", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	// Child processes receive real user folders and system executables. They
	// receive only synthetic credentials and cannot fetch public release data.
	var env []string
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "COMSPEC", "USERNAME", "USERDOMAIN"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	env = append(env, "OPENAI_API_KEY=sk-fake-windows-fixture", "OPENAI_BASE_URL="+api.URL+"/v1",
		"HTTPS_PROXY="+proxy.URL, "HTTP_PROXY="+proxy.URL, "NO_PROXY=localhost,127.0.0.1")
	state := filepath.Join(filepath.Dir(managed), ".update", "state.json")
	machine := exec.CommandContext(t.Context(), managed, "models", "retrieve", "fixture-model", "--format", "json")
	machine.Env = env
	out, err := machine.Output()
	if err != nil || !json.Valid(out) || !strings.Contains(string(out), "fixture-model") {
		t.Fatalf("redirected JSON API command: %v", err)
	}
	seed, err := os.ReadFile(state)
	var initial struct{ Checked, Starting int64 }
	if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && (json.Unmarshal(seed, &initial) != nil || initial.Checked != 0 || initial.Starting != 0) {
		t.Fatal("machine command or setup launched the updater", err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	owner := exec.CommandContext(ctx, testBinary, "-test.run=^TestMainWindowsArchiveConsoleOwner$", "--", managed)
	owner.Env = append(env, "OPENAI_ARCHIVE_TEST_CONSOLE=1")
	owner.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if out, err := owner.CombinedOutput(); err != nil {
		t.Fatalf("native console command: %v %s", err, out)
	}
	select {
	case attempt := <-attempts:
		if attempt != "CONNECT api.github.com:443" {
			t.Fatalf("unexpected release request: %s", attempt)
		}
	case <-ctx.Done():
		t.Fatal("ordinary API command never attempted release discovery")
	}
	raw, err := os.ReadFile(state)
	var receipt struct {
		Checked int64 `json:"checked"`
	}
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Checked <= 0 || requests.Load() != 2 {
		t.Fatal("missing automatic update receipt or incorrect foreground API request count", err)
	}
}

func TestMainWindowsArchiveConsoleOwner(t *testing.T) {
	if os.Getenv("OPENAI_ARCHIVE_TEST_CONSOLE") != "1" {
		t.Skip("helper for the disposable standard user's native console")
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		t.Fatal("missing native console target")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		t.Fatal("expected native console handles")
	}
	cmd := exec.CommandContext(t.Context(), os.Args[len(os.Args)-1], "models", "retrieve", "fixture-model")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, output
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
