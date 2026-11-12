//go:build windows

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/windows"
)

const admin753BodyCanary = "PRIVATE753BODY"
const admin753ScreenMarker = "ADMIN753-NATIVE-ROWS-PASSED"

func TestAdmin753Shipping401(t *testing.T) {
	adminConsoleTestWorker(t, "TestAdmin753Shipping401", "./internal/commands/admin", func(t *testing.T) {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != http.MethodGet || r.URL.Path != "/v1/organization/projects" ||
				r.URL.RawQuery != "limit=1" || r.Header.Get("Authorization") != "Bearer "+adminConsoleSyntheticKey {
				t.Error("unexpected request method, route, limit, or synthetic authorization")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, err := io.WriteString(w, `{"error":{"message":"`+admin753BodyCanary+" "+adminConsoleSyntheticKey+`"}}`)
			if err != nil {
				t.Error("loopback response write failed")
			}
		}))
		defer server.Close()

		session := adminConsoleStart(t, executable, "TestAdmin753ScreenHost", []string{"OPENAI_BASE_URL=" + server.URL + "/v1"})
		hello := session.hello
		if hello.Original.Stdin&windows.ENABLE_ECHO_INPUT == 0 {
			t.Fatal("expected echo before the prompt")
		}
		session.send(t, map[string]any{"action": "start", "nonce": session.nonce})
		active := session.receive(t)
		if active.Event != "active" || !adminConsoleHiddenInput(active.Active.Stdin) {
			t.Fatal("hidden input was not active")
		}
		session.waitPrompt(t)
		session.write(t, "synthetic_401", adminConsoleSyntheticKey+"\r")
		completed := session.receive(t)
		if completed.Event != "completed" || completed.Exit != 1 || completed.Restored != hello.Original {
			t.Error("expected exit 1 and restored stdin, stdout, and stderr modes")
		}
		session.send(t, map[string]any{"action": "ack"})
		output := session.finish(t)
		if requests.Load() != 1 {
			t.Fatal("expected exactly one request")
		}
		if !utf8.ValidString(output) || !strings.ContainsRune(output, '…') {
			t.Error("ConPTY stream lacks valid UTF-8 or the U+2026 character")
		}
		if strings.Contains(ansi.Strip(output), admin753BodyCanary) {
			t.Error("private response text appeared in console output")
		}
		if !strings.Contains(ansi.Strip(output), admin753ScreenMarker) {
			t.Error("native screen observer did not confirm expected rows")
		}
	})
}

func TestAdmin753ScreenHost(t *testing.T) {
	if os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL") == "" {
		t.Skip("private console worker required")
	}
	// The stock host runs the actual dispatch child inside its own 120 by 40 console.
	TestMainDispatchAdminSetupConsoleHost(t)
	if t.Failed() {
		return
	}
	cells, width, err := readOutputConsoleCells(os.Stderr)
	if err != nil || width != 120 || len(cells)%width != 0 {
		t.Fatal("cannot read complete 120-column Windows console rows")
	}
	var rows []string
	for i := 0; i < len(cells); i += width {
		rows = append(rows, strings.TrimRight(string(utf16.Decode(cells[i:i+width])), " "))
	}
	screen := "\n" + strings.Join(rows, "\n") + "\n"
	origin := strings.TrimSuffix(os.Getenv("OPENAI_BASE_URL"), "/v1")
	prompt := strings.Join([]string{
		"Check admin access", "",
		"Paste your admin API key. It stays hidden on screen.",
		"Press Enter to check the key with " + origin + ".", "",
		"Need a key? Organization owners can create one here.",
		"  https://platform.openai.com/settings/organization/admin-keys", "",
		"Press Ctrl+C to cancel.", "", "Admin API key (hidden):",
	}, "\n")
	want := "\n" + prompt + "\nChecking admin access…\n\n" +
		"The server rejected your admin API key (401).\n" +
		"Check that you copied an admin API key for the right organization.\n" +
		"Run openai setup admin to try again.\n"
	if strings.Count(screen, want) != 1 {
		t.Fatal("native prompt, progress, or ordered error rows differ")
	}
	if strings.Contains(screen, "sk-admin-SYNTHETIC") || strings.Contains(screen, admin753BodyCanary) {
		t.Fatal("synthetic credential or response body appeared on the screen")
	}
	t.Log(admin753ScreenMarker)
}
