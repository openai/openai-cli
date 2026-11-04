//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
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

func requireWindowsArchiveAccount(t *testing.T) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil || !strings.EqualFold(account, "OpenAIArchiveTest") || os.Getenv("GITHUB_ACTIONS") != "true" || windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("refusing HKCU enrollment outside the dedicated non-elevated hosted account")
	}
}

// Positive enrollment edits HKCU. Run only in the dedicated hosted account;
// local Windows developers can run the remaining archive tests without enrolling.
func TestMainWindowsArchiveOrdinarySetup(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_FIXTURE") != "1" {
		t.Skip("requires the disposable standard-user Windows release fixture")
	}
	requireWindowsArchiveAccount(t)
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
	public := os.Getenv("OPENAI_CLI_WINDOWS_ARCHIVE_BASELINE")
	if !filepath.IsAbs(public) {
		t.Fatal("missing the public ZIP baseline")
	}
	plain := func(args ...string) (string, error) {
		cmd := exec.Command(public, args...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "OPENAI_CLI_NO_UPDATE_CHECK=1")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := os.Lstat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy comparison requires a pristine account", err)
	}
	legacyVersion, err := plain("--version")
	if err != nil || legacyVersion != "openai version 1.48.1" {
		t.Fatal("public baseline version mismatch", err)
	}
	failure, err := plain("install", "--user")
	var failed *exec.ExitError
	if !errors.As(err, &failed) || failed.ExitCode() == 0 {
		t.Fatal("the legacy release unexpectedly accepted enrollment")
	}
	diagnostic, _, _ := strings.Cut(strings.ReplaceAll(failure, "\r\n", "\n"), "\n")
	if diagnostic != "The --user option is not available for this command." {
		t.Fatal("unrecognized public baseline diagnostic")
	}
	if _, err := os.Lstat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy portable invocation changed managed state", err)
	}
	t.Logf("Windows ZIP before (published v1.48.1)\n> openai --version\n%s\n> openai install --user\n%s\nExit %d", legacyVersion, diagnostic, failed.ExitCode())
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
	const restrictedZone = "[ZoneTransfer]\r\nZoneId=4\r\n"
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
	verifyWindowsFirstPrivateVerifier(t, older, area, managed)
	verifyWindowsSetupWait(t, older, managed)
	if err := os.WriteFile(managed+":Zone.Identifier", []byte(restrictedZone), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\nHostUrl=https://fixture.invalid/source\r\n"), 0600); err != nil {
		t.Fatal(err)
	}

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
if (![string]::Equals((Get-Command openai).Path,$env:ARCHIVE_OLD,[StringComparison]::OrdinalIgnoreCase)) {throw 'wrong initial selection'}
` + paste
	// A successful Explorer setup must exit on its own. Both handles belong
	// to a real console. No keypress is written to its input buffer.
	ctxClick, cancelClick := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancelClick()
	screen := filepath.Join(area, "install-console.txt")
	envClick := append(os.Environ(), "OPENAI_ARCHIVE_TEST_MODE=install", "OPENAI_ARCHIVE_TEST_SCREEN="+screen, "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9")
	runWindowsUpdateConsole(t, ctxClick, installer, extracted, envClick)
	output, err := os.ReadFile(screen)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := windowsInstallerTranscript(string(output))
	if err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(managed + ":Zone.Identifier")
	if err != nil || string(marker) != restrictedZone {
		t.Fatal("explicit re-enrollment lowered the previous download zone", err)
	}
	t.Logf("Windows ZIP after (candidate archive)\n> Install.cmd\n%s\nExit 0", strings.Join(transcript, "\n"))
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
	if err != nil || string(content) != restrictedZone {
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

// Test the ordinary public download independently of tools installed on the
// hosted runner. Only the retry runs with the deliberately unreachable proxy.
func verifyWindowsFirstPrivateVerifier(t *testing.T, cli, emptyPath, managed string) {
	t.Helper()
	cmd := func(path string, args ...string) *exec.Cmd {
		c := exec.CommandContext(t.Context(), cli, args...)
		// Only forward OS loader, user-directory, proxy, and TLS settings.
		// No GitHub credentials or pre-acquired archive can satisfy this check.
		for _, key := range []string{"SystemRoot", "WINDIR", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "HOME", "TEMP", "TMP", "TMPDIR", "PATHEXT", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
			if value, ok := os.LookupEnv(key); ok {
				c.Env = append(c.Env, key+"="+value)
			}
		}
		c.Env = append(c.Env, "PATH="+path)
		return c
	}
	status := func(path string) string {
		t.Helper()
		probe := cmd(path, "__openai_archive_verifier")
		probe.Env = append(probe.Env, "OPENAI_CLI_ARCHIVE_INSTALL=1")
		out, err := probe.Output()
		value := strings.TrimSpace(string(out))
		if err != nil || (value != "ready" && value != "missing" && value != "unavailable") {
			t.Fatal("invalid verifier capability result", err)
		}
		return value
	}
	path, lookupErr := exec.LookPath("gh")
	t.Logf("Disposable account system verifier: PATH lookup=%t, CLI capability=%s", lookupErr == nil && filepath.IsAbs(path), status(os.Getenv("PATH")))
	if status(emptyPath) != "missing" {
		t.Fatal("first transfer requires a pristine account without a usable verifier")
	}

	start := cmd(emptyPath, "install", "--user")
	var diag bytes.Buffer
	start.Stderr = &diag
	out, err := start.Output()
	if err != nil {
		// Keep transport details, signed URLs, and paths out of hosted logs.
		for _, hint := range []string{"Could not download the GitHub CLI verifier", "GitHub TLS verification failed", "failed its checksum check", "Could not access the private verifier directory"} {
			if strings.Contains(diag.String(), hint) {
				t.Fatalf("initial Windows ZIP setup: %v (%s)", err, hint)
			}
		}
		t.Fatalf("initial Windows ZIP setup: %v", err)
	}
	const progress = "Downloading GitHub CLI for automatic update verification..."
	if strings.TrimSpace(diag.String()) != progress || !strings.EqualFold(strings.TrimSpace(string(out)), managed) {
		t.Fatal("first setup must print exactly one download start and one installed path")
	}
	// With no system gh, the read-only probe checks the private executable
	// against its compiled SHA-256 and exercises its required capabilities.
	if status(emptyPath) != "ready" {
		t.Fatal("the downloaded private verifier failed its pin or capability check")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(managed), ".update-verifier", "gh-*.exe"))
	if err != nil || len(files) != 1 {
		t.Fatal("expected exactly one private verifier executable", err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	f.Close()
	if err != nil || size == 0 {
		t.Fatal("could not hash the downloaded verifier", err)
	}
	mark, err := os.ReadFile(files[0] + ":Zone.Identifier")
	if err != nil || string(mark) != "[ZoneTransfer]\r\nZoneId=3\r\n" {
		t.Fatal("private verifier lacks its Internet zone", err)
	}
	t.Logf("First private verifier transfer: one download, %d executable bytes, pinned SHA-256 %x, Internet zone 3", size, hash.Sum(nil))

	repeat := cmd(emptyPath, "install", "--user")
	repeat.Env = append(repeat.Env, "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost")
	diag.Reset()
	repeat.Stderr = &diag
	out, err = repeat.Output()
	if err != nil || diag.Len() != 0 || !strings.EqualFold(strings.TrimSpace(string(out)), managed) {
		t.Fatal("verified cached setup must succeed quietly through a closed proxy", err)
	}
	t.Log("Cached verifier: offline repeat succeeded without a download message")
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
