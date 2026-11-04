//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/app"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestMainWindowsArchiveInstallHelp(t *testing.T) {
	dir, name := buildNativeShellCLI(t)
	exe := filepath.Join(dir, name)
	for _, args := range [][]string{{"install", "--help"}, {"help", "install"}} {
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), "OPENAI_BASE_URL=bad://private-fixture")
		out, err := cmd.CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("--user")) {
			t.Fatalf("missing Windows setup help: %v %s", err, out)
		}
	}
	const operand = "PRIVATE_FIXTURE_INSTALL_OPERAND"
	for _, args := range [][]string{{"install"}, {"install", "--user=false"}, {"install", "--user", operand}} {
		cmd := exec.Command(exe, args...)
		if out, err := cmd.CombinedOutput(); err == nil || bytes.Contains(out, []byte(operand)) {
			t.Fatal("setup accepted or disclosed an invalid argument")
		}
	}
}

// Positive enrollment edits HKCU. Run only in the dedicated hosted account;
// local Windows developers can run the remaining archive tests without enrolling.
func TestMainWindowsArchiveOrdinarySetup(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_FIXTURE") != "1" {
		t.Skip("requires the disposable standard-user Windows release fixture")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil || !strings.EqualFold(account, "OpenAIArchiveTest") || os.Getenv("GITHUB_ACTIONS") != "true" || windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("refusing HKCU enrollment outside the dedicated non-elevated hosted account")
	}
	archive, setupScript := os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_BINARY"), os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_SCRIPT")
	olderArchive := os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_OLDER_BINARY")
	if !filepath.IsAbs(archive) || !filepath.IsAbs(setupScript) || !filepath.IsAbs(olderArchive) {
		t.Fatal("provide the candidate release-stamped Windows executable and Install.cmd")
	}
	area := t.TempDir()
	extracted := filepath.Join(area, "Extracted 日本語 ZIP's files & commands")
	if err := os.Mkdir(extracted, 0700); err != nil {
		t.Fatal(err)
	}
	copyFile := func(source, dest string) []byte {
		t.Helper()
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, content, 0700); err != nil {
			t.Fatal(err)
		}
		return content
	}
	entry := filepath.Join(extracted, "openai.exe")
	original := sha256.Sum256(copyFile(archive, entry))
	olderDir := filepath.Join(area, "older ZIP")
	if err := os.Mkdir(olderDir, 0700); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(olderDir, "openai.exe")
	olderHash := sha256.Sum256(copyFile(olderArchive, older))
	if olderHash == original {
		t.Fatal("the release fixtures must have different bytes")
	}
	installer := filepath.Join(extracted, "Install.cmd")
	copyFile(setupScript, installer)
	managed := filepath.Join(os.Getenv("LOCALAPPDATA"), "OpenAI", "CLI", "bin", "openai.exe")
	cliDir := filepath.Dir(filepath.Dir(managed))
	if _, err := os.Lstat(cliDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the disposable account already has an installation or inaccessible destination")
	}
	t.Cleanup(func() {
		// The final detached worker can still be exiting after its network
		// check. Windows keeps its mapped helper image locked until exit.
		deadline := time.Now().Add(15 * time.Second)
		for {
			err := os.RemoveAll(cliDir)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Error(err)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	const zone = "[ZoneTransfer]\r\nZoneId=3\r\n"
	if err := os.WriteFile(entry+":Zone.Identifier", []byte(zone), 0600); err != nil {
		t.Fatal(err)
	}
	staleBin := filepath.Join(area, "old Go bin")
	if err := os.Mkdir(staleBin, 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(staleBin, "openai.exe")
	if err := os.WriteFile(stale, []byte("historical copy"), 0600); err != nil {
		t.Fatal(err)
	}
	userEnv, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { userEnv.Close() })
	old, kind, err := userEnv.GetStringValue("Path")
	hasOld := err == nil
	if !hasOld && !errors.Is(err, registry.ErrNotExist) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		switch {
		case !hasOld:
			err = userEnv.DeleteValue("Path")
		case kind == registry.EXPAND_SZ:
			err = userEnv.SetExpandStringValue("Path", old)
		default:
			err = userEnv.SetStringValue("Path", old)
		}
		if err != nil {
			t.Error(err)
		}
	})
	if err := userEnv.SetExpandStringValue("Path", staleBin+";"+old); err != nil {
		t.Fatal(err)
	}

	setup := func(exe string) ([]byte, error) {
		cmd := exec.Command(exe, "install", "--user")
		cmd.Env = append(os.Environ(), "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=localhost,127.0.0.1")
		return cmd.CombinedOutput()
	}
	verifyWindowsVerifierDownloadProgress(t, older, area)
	const olderVersion = "openai version 0.0.1"
	wantVersion := "openai version " + app.Version
	if out, err := exec.Command(older, "--version").Output(); err != nil || strings.TrimSpace(string(out)) != olderVersion {
		t.Fatalf("older release fixture has the wrong displayed version: %v %s", err, out)
	}
	if out, err := setup(older); err != nil || !strings.EqualFold(strings.TrimSpace(string(out)), managed) {
		t.Fatalf("initial older ZIP setup: %v %s", err, out)
	}
	verifyWindowsSetupWait(t, older, managed)
	statePath := filepath.Join(filepath.Dir(managed), ".update", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	managedName, err := windows.UTF16PtrFromString(managed)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another executable loader that denies deletion or replacement.
	busy, err := windows.CreateFile(managedName, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	lockedOutput, installErr := setup(entry)
	windows.CloseHandle(busy)
	if installErr == nil || !bytes.Contains(lockedOutput, []byte("in use")) {
		t.Fatalf("locked replacement: %v %s", installErr, lockedOutput)
	}
	unchanged, err := os.ReadFile(managed)
	if err != nil || sha256.Sum256(unchanged) != olderHash {
		t.Fatal("locked setup changed the executable", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("locked setup changed its receipt", err)
	}
	// Simulate an interruption immediately after A and its stream arrived but
	// before A's shared updater receipt was written. The next paste repairs it.
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}

	// Test the native paste even on hosts whose policy rejects downloaded scripts.
	paste := documentedWindowsArchivePaste(t, archive)
	current := `$ErrorActionPreference='Stop'
if ((Get-Command openai).Path -ine $env:ARCHIVE_OLD) {throw 'wrong initial selection'}
` + paste
	click := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "Install.cmd")
	click.Dir = extracted
	click.Stdin = strings.NewReader("\r\n")
	click.Env = append(os.Environ(), "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9")
	if out, err := click.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("OpenAI CLI setup finished.")) {
		t.Fatalf("Explorer installer failed: %v %s", err, out)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	for _, shellName := range []string{"powershell.exe", "pwsh.exe"} {
		shell, err := exec.LookPath(shellName)
		if err != nil {
			t.Fatal(err)
		}
		env := append(os.Environ(), "PATH="+staleBin+";"+os.Getenv("PATH"), "ARCHIVE_OLD="+stale, "ARCHIVE_ENTRY="+entry, "ARCHIVE_TARGET="+managed, "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost")
		cmd := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", current)
		cmd.Dir, cmd.Env = extracted, env
		if out, err := cmd.CombinedOutput(); err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), wantVersion) {
			t.Fatalf("current %s: %v %s", shellName, err, out)
		}
		alias := `$ErrorActionPreference='Stop'
Set-Alias openai Write-Output
try {` + paste + `} catch {
    if ($_.Exception.Message -like '*overrides openai*' -and (Get-Command openai).CommandType -eq 'Alias') { 'alias preserved'; exit 0 }
    throw
}
throw 'setup ignored the conflicting alias'`
		cmd = exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", alias)
		cmd.Dir, cmd.Env = extracted, env
		if out, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("alias preserved")) {
			t.Fatalf("conflicting alias in %s: %v %s", shellName, err, out)
		}
		// Rebuild from HKLM/HKCU through CreateEnvironmentBlock. Inheriting our
		// prepared parent PATH would give a false fresh-shell pass.
		fresh, err := token.Environ(false)
		if err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; (Get-Command openai).Path; openai --version; exit $LASTEXITCODE`)
		cmd.Dir, cmd.Env = area, fresh
		out, err := cmd.CombinedOutput()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if err != nil || len(lines) != 2 || !strings.EqualFold(strings.TrimSpace(lines[0]), managed) || strings.TrimSpace(lines[1]) != wantVersion {
			t.Fatalf("fresh %s: %v %s", shellName, err, out)
		}
	}
	fresh, err := token.Environ(false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Getenv("ComSpec"), "/d", "/c", "where.exe openai.exe")
	cmd.Dir, cmd.Env = area, fresh
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.EqualFold(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), managed) {
		t.Fatalf("fresh cmd.exe: %v %s", err, out)
	}
	for _, name := range []string{entry, managed} {
		content, err := os.ReadFile(name)
		if err != nil || sha256.Sum256(content) != original {
			t.Fatalf("unexpected installed or portable bytes: %v", err)
		}
	}
	content, err := os.ReadFile(managed + ":Zone.Identifier")
	if err != nil || string(content) != zone {
		t.Fatal("managed executable lost its download zone", err)
	}
	content, err = os.ReadFile(stale)
	if err != nil || string(content) != "historical copy" {
		t.Fatal("setup modified the competing executable", err)
	}
	if out, err := setup(older); err != nil {
		t.Fatalf("rerunning the older ZIP: %v %s", err, out)
	}
	content, err = os.ReadFile(managed)
	if err != nil || sha256.Sum256(content) != original {
		t.Fatal("older ZIP downgraded the installed CLI", err)
	}
	var receipt struct {
		Applied   string
		Announced bool
	}
	data, err := os.ReadFile(statePath)
	if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Applied == "" || receipt.Applied == "0.0.1" || !receipt.Announced {
		t.Fatal("new ZIP lacks the expected managed receipt", err)
	}
	verifyWindowsOrdinaryUpdateAdmission(t, managed)
}

func verifyWindowsVerifierDownloadProgress(t *testing.T, exe, area string) {
	t.Helper()
	release := make(chan struct{})
	requested := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case requested <- req.Method + " " + req.Host:
		default:
		}
		<-release
		http.Error(w, "offline fixture", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "install", "--user")
	cmd.Env = append(os.Environ(), "PATH="+area, "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=")
	var out bytes.Buffer
	cmd.Stdout = &out
	diag, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	seen, totals := make(chan struct{}, 1), make(chan int, 1)
	go func() {
		scan := bufio.NewScanner(diag)
		count := 0
		for scan.Scan() {
			if scan.Text() == "Downloading GitHub CLI for automatic update verification..." {
				count++
				if count == 1 {
					seen <- struct{}{}
				}
			}
		}
		totals <- count
	}()
	select {
	case <-seen:
	case <-ctx.Done():
		unblock()
		cmd.Wait()
		t.Fatal("native setup withheld progress while the download was active")
	}
	select {
	case destination := <-requested:
		if destination != "CONNECT github.com:443" {
			t.Error("unexpected verifier download destination")
		}
	case <-ctx.Done():
		t.Error("the installer never attempted its announced verifier download")
	}
	unblock()
	count := <-totals
	if err := cmd.Wait(); err == nil || count != 1 || out.Len() != 0 {
		t.Fatal("failed enrollment must report one download start and no installed path")
	}
}
