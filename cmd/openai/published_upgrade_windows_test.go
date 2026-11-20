//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type windowsPublishedState struct {
	Checked       int64
	Applied       string
	AppliedHash   string `json:"applied_hash"`
	AppliedCommit string `json:"applied_commit"`
	Pending       *struct {
		Release      struct{ Version, Commit, Digest string }
		PreviousHash string `json:"previous_hash"`
		BinaryHash   string `json:"binary_hash"`
	}
}

// Observe genuine public updates without calling the worker, altering its
// state, changing clocks, or selecting a successor for the product.
func TestMainWindowsArchivePublishedUpgrade(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WINDOWS_PUBLISHED_VERIFY") != "1" {
		t.Skip("manual public release verification")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("requires a disposable hosted standard account")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil || !strings.EqualFold(name, "OpenAIArchiveTest") {
		t.Fatal("wrong verification account")
	}
	starter, successor := os.Getenv("OPENAI_WINDOWS_STARTER_TAG"), os.Getenv("OPENAI_WINDOWS_SUCCESSOR_TAG")
	stable := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	if !stable.MatchString(starter) || !stable.MatchString(successor) || starter == successor {
		t.Fatal("invalid qualification tags")
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(local, "OpenAI", "CLI", "bin", "openai.exe")
	before, err := windowsPublishedHash(managed)
	if err != nil {
		t.Fatal(err)
	}
	if before != os.Getenv("OPENAI_WINDOWS_PUBLIC_STARTER_SHA") {
		t.Fatal("starter differed from authenticated A before its first ordinary request")
	}
	statePath := filepath.Join(filepath.Dir(managed), ".update", "state.json")
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	fresh, err := token.Environ(false)
	if err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	selection := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `if (![string]::Equals((Get-Command openai).Path,$env:OPENAI_WINDOWS_EXPECTED_EXE,[StringComparison]::OrdinalIgnoreCase)) {exit 22}`)
	selection.Env = append(append([]string{}, fresh...), "OPENAI_WINDOWS_EXPECTED_EXE="+managed)
	if err := selection.Run(); err != nil {
		t.Fatal("the unaltered fresh environment selected another CLI", err)
	}
	requests, env := windowsUpdateAPI(t, fresh)
	if os.Getenv("OPENAI_WINDOWS_PRIVATE_VERIFIER") == "1" {
		for i, e := range env {
			key, value, ok := strings.Cut(e, "=")
			if !ok || !strings.EqualFold(key, "PATH") {
				continue
			}
			var keep []string
			for _, entry := range filepath.SplitList(value) {
				if _, err := os.Stat(filepath.Join(entry, "gh.exe")); errors.Is(err, os.ErrNotExist) {
					keep = append(keep, entry)
				} else if err != nil {
					t.Fatal(err)
				}
			}
			env[i] = key + "=" + strings.Join(keep, ";")
		}
		env = append(env, "OPENAI_WINDOWS_REQUIRE_PRIVATE_GH=1")
	}
	env = append(env, "NO_PROXY=127.0.0.1,localhost")
	// Read the installed release's own command tree. Explicit help or settings
	// commands do not count as ordinary use or start the update worker.
	supportsOptIn := func() bool {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), managed, "--help")
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("could not read the installed release's help", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if fields := strings.Fields(line); len(fields) > 1 && fields[0] == "updates" {
				return true
			}
		}
		return false
	}
	starterOptIn := supportsOptIn()
	if starterOptIn {
		requireWindowsUpdateMode(t, managed, "manual")
		initial := waitWindowsPublishedState(t, statePath, func(state windowsPublishedState) bool { return state.Applied == starter[1:] })
		if initial.Checked != 0 || initial.Pending != nil {
			t.Fatal("fresh manual starter already attempted an automatic upgrade")
		}
		cmd := exec.CommandContext(t.Context(), managed, "updates", "enable")
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("could not enroll the public starter: %v: %s", err, out)
		}
		requireWindowsUpdateMode(t, managed, "automatic")
	}
	work := t.TempDir()
	run := func(mode string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
		defer cancel()
		screen := filepath.Join(work, "console.txt")
		options := append(append([]string{}, env...), "OPENAI_ARCHIVE_TEST_MODE="+mode, "OPENAI_ARCHIVE_TEST_SCREEN="+screen)
		runWindowsUpdateConsole(t, ctx, managed, work, options)
		data, err := os.ReadFile(screen)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(screen); err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	request := func() {
		t.Helper()
		screen := run("request")
		if !strings.Contains(screen, "fixture-model") || strings.Contains(screen, "Updated to ") {
			t.Fatal("ordinary request lost its API output or printed an update confirmation")
		}
	}
	request()
	prepared := waitWindowsPublishedState(t, statePath, func(state windowsPublishedState) bool { return state.Checked > 0 && state.Pending != nil })
	if prepared.Pending.Release.Version != successor[1:] || prepared.Pending.PreviousHash != before {
		t.Fatal("public discovery selected another release or source executable")
	}
	expected := prepared.Pending.BinaryHash
	stagedPath := filepath.Join(filepath.Dir(statePath), "next")
	staged, err := windowsPublishedHash(stagedPath)
	if err != nil || staged != expected || staged == before {
		t.Fatal("prepared successor does not match its verified receipt", err)
	}
	requirePublishedInternetZone(t, stagedPath)
	request()
	applied := waitWindowsPublishedState(t, statePath, func(state windowsPublishedState) bool {
		if state.Applied != successor[1:] || state.AppliedHash != expected {
			return false
		}
		actual, err := windowsPublishedHash(managed)
		return err == nil && actual == expected
	})
	requirePublishedInternetZone(t, managed)
	if requests.Load() != 2 || applied.AppliedCommit != prepared.Pending.Release.Commit {
		t.Fatal("wrong API request count or applied commit")
	}
	if starterOptIn {
		requireWindowsUpdateMode(t, managed, "automatic")
	}
	const migration = "Updates are now manual. To enable automatic updates again, run openai updates enable."
	migrates := !starterOptIn && supportsOptIn()
	expectedNotice := "Updated to " + successor + "."
	if migrates {
		expectedNotice = migration
	}
	firstBare, secondBare := run("bare"), run("bare")
	if !strings.Contains(firstBare, expectedNotice) || strings.Count(firstBare, "Updated to ")+strings.Count(firstBare, migration) != 1 ||
		strings.Contains(secondBare, "Updated to ") || strings.Contains(secondBare, migration) {
		t.Fatal("expected only the release-specific notice on the first bare invocation")
	}
	if migrates {
		requireWindowsUpdateMode(t, managed, "manual")
		manual := waitWindowsPublishedState(t, statePath, func(state windowsPublishedState) bool { return state.Applied == successor[1:] })
		if manual.Pending != nil || manual.Checked != 0 {
			t.Fatal("the transition release continued automatic updates without explicit enrollment")
		}
	}
	report := struct{ Architecture, Starter, Successor, StarterSHA256, ExecutableSHA256, ArchiveSHA256, Commit string }{
		runtime.GOARCH, starter, successor, before, expected, prepared.Pending.Release.Digest, applied.AppliedCommit,
	}
	output, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	destination := os.Getenv("OPENAI_WINDOWS_PUBLIC_RECEIPT")
	if !filepath.IsAbs(destination) {
		t.Fatal("missing public verification receipt path")
	}
	if err := os.WriteFile(destination, output, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Two ordinary API requests upgraded %s to %s on Windows %s. The CLI showed its notice once: %s", starter, successor, runtime.GOARCH, expectedNotice)
}

func waitWindowsPublishedState(t *testing.T, path string, check func(windowsPublishedState) bool) windowsPublishedState {
	t.Helper()
	timeout := time.NewTimer(150 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	// Open relative to a Root so observation shares DELETE access with Windows
	// atomic renames, including while the worker publishes a receipt.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var last windowsPublishedState
	for {
		data, err := root.ReadFile(filepath.Base(path))
		if err == nil {
			var current windowsPublishedState
			if err := json.Unmarshal(data, &current); err != nil {
				t.Fatal("unreadable private update state", err)
			}
			last = current
			if check(current) {
				return current
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("could not observe private update state", err)
		}
		select {
		case <-timeout.C:
			t.Fatalf("public worker did not finish: checked=%t pending=%t applied=%q", last.Checked > 0, last.Pending != nil, last.Applied)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-tick.C:
		}
	}
}

func windowsPublishedHash(path string) (string, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func requirePublishedInternetZone(t *testing.T, path string) {
	t.Helper()
	mark, err := os.ReadFile(path + ":Zone.Identifier")
	if err != nil || (string(mark) != "[ZoneTransfer]\r\nZoneId=3\r\n" && string(mark) != "[ZoneTransfer]\r\nZoneId=4\r\n") {
		t.Fatal("public executable lost its Windows attachment security zone", err)
	}
}
