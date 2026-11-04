//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The ephemeral runner creates one inert, exclusively held file in an existing
// machine PATH tool directory. No machine registry or policy value changes.
func TestMainWindowsArchiveMachinePathConflict(t *testing.T) {
	conflict := os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_CONFLICT")
	if conflict == "" {
		t.Skip("requires the disposable standard-user machine PATH fixture")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil || !strings.EqualFold(account, "OpenAIPathTest") || os.Getenv("GITHUB_ACTIONS") != "true" || windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("requires the disposable standard account")
	}
	archive := os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_BINARY")
	paste := documentedWindowsArchivePaste(t, archive)
	dir := t.TempDir()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	launcher, err := os.ReadFile(os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_SCRIPT"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Install.cmd"), launcher, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	// A pipe represents a user acknowledging the actual launcher's pause.
	// Healthy attached-console setup has separate coverage without a keypress.
	click := exec.CommandContext(ctx, os.Getenv("ComSpec"), "/d", "/c", "Install.cmd")
	click.Dir = dir
	input, err := click.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := click.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var warning bytes.Buffer
	click.Stderr = &warning
	if err := click.Start(); err != nil {
		t.Fatal(err)
	}
	acknowledged := false
	lines := bufio.NewScanner(output)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == "OpenAI CLI is installed. Review the machine PATH instruction above." {
			acknowledged = true
			if _, err := input.Write([]byte("\r\n")); err != nil {
				t.Fatal(err)
			}
			input.Close()
		}
	}
	if err := click.Wait(); err != nil || lines.Err() != nil || !acknowledged ||
		!strings.Contains(warning.String(), "New terminals select the machine PATH copy first") {
		t.Fatal("Explorer setup did not retain the machine PATH recovery instruction", err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	for _, name := range []string{"powershell.exe", "pwsh.exe"} {
		shell, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		run := func(code string) (string, string) {
			t.Helper()
			fresh, err := token.Environ(false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", code)
			cmd.Dir = dir
			cmd.Env = append(fresh, "OPENAI_ARCHIVE_MACHINE_FIXTURE="+conflict, "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				code := "unavailable"
				if cmd.ProcessState != nil {
					code = strconv.Itoa(cmd.ProcessState.ExitCode())
				}
				t.Fatalf("machine conflict in %s: exit=%s; stdout present=%t; stderr present=%t", name, code, stdout.Len() != 0, stderr.Len() != 0)
			}
			return strings.TrimSpace(stdout.String()), stderr.String()
		}
		check := `if ((Get-Command openai).Path -ine $env:OPENAI_ARCHIVE_MACHINE_FIXTURE) { throw 'expected the original machine command' }`
		out, diagnostic := run("$ErrorActionPreference='Stop'; " + check + "\n" + paste)
		if !strings.Contains(out, "openai version ") || !strings.Contains(diagnostic, "New terminals select the machine PATH copy first") {
			t.Fatalf("%s lost the current CLI or new-terminal warning", name)
		}
		run(check)
	}
}

func documentedWindowsArchivePaste(t *testing.T, archive string) string {
	t.Helper()
	guide, err := os.ReadFile(filepath.Join(filepath.Dir(archive), "installation.md"))
	if err != nil {
		t.Fatal(err)
	}
	var paste string
	for _, block := range strings.Split(strings.ReplaceAll(string(guide), "\r\n", "\n"), "```powershell\n")[1:] {
		code, _, closed := strings.Cut(block, "```")
		if closed && strings.Contains(code, `.\openai.exe install --user`) {
			if paste != "" {
				t.Fatal("ambiguous documented ZIP paste")
			}
			paste = code
		}
	}
	if paste == "" {
		t.Fatal("missing the documented ZIP setup paste")
	}
	return paste
}

func verifyWindowsSetupWait(t *testing.T, exe, managed string) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(managed), ".update", ".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	var overlap windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "install", "--user")
	cmd.Env = append(os.Environ(), "HTTPS_PROXY=http://127.0.0.1:9", "HTTP_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost")
	var out bytes.Buffer
	cmd.Stdout = &out
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	observed := false
	lines := bufio.NewScanner(stderr)
	for lines.Scan() {
		if lines.Text() == "Waiting for the current OpenAI CLI update to finish..." {
			if observed {
				t.Fatal("duplicate setup wait diagnostic")
			}
			observed = true
			if err := windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlap); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := cmd.Wait(); err != nil || !observed || lines.Err() != nil || !strings.EqualFold(strings.TrimSpace(out.String()), managed) {
		t.Fatal("setup did not finish after the existing worker released its lock", err)
	}
}
