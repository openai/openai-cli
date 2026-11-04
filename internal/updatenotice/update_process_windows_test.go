//go:build windows

package updatenotice

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/windowsfile"
	"golang.org/x/sys/windows"
)

// Use a disposable child of the actual user cache. Hosted agents can put
// RUNNER_TEMP on a shared volume, which the production installer must reject.
func windowsUpdateFixture(t *testing.T) (bin, state string) {
	t.Helper()
	local, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.MkdirTemp(local, "openai-cli-native-update-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(fixture); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("LOCALAPPDATA", fixture)
	path, err := windowsInstallPath()
	if err != nil {
		t.Fatal(err)
	}
	bin = filepath.Dir(path)
	for _, dir := range []string{filepath.Join(fixture, "OpenAI"), filepath.Dir(bin), bin, filepath.Join(bin, ".update")} {
		f, err := windowsfile.CreatePrivateDirectory(dir)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	for _, key := range []string{"OPENAI_CLI_NO_UPDATE_CHECK", "OPENAI_CLI_NO_AUTO_UPDATE", "OPENAI_CLI_VERSION_PIN"} {
		t.Setenv(key, "")
	}
	return bin, filepath.Join(bin, ".update")
}

func TestWindowsArchivePrivatePaths(t *testing.T) {
	bin, state := windowsUpdateFixture(t)
	managed, err := openWindowsManagedBin()
	if err != nil {
		t.Fatal(err)
	}
	managed.Close()
	target := filepath.Join(bin, "openai.exe")
	if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsUpdatePath(target, false); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(state, "aliased")
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsUpdatePath(target, false); err == nil {
		t.Fatal("accepted a multiply linked executable")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsUpdatePath(target, false); err == nil {
		t.Fatal("accepted a foreign-write DACL")
	}
	// A junction is a real reparse point and requires no symlink privilege.
	junction := filepath.Join(bin, "junction")
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", junction, state)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
	if err := validateWindowsUpdatePath(junction, true); err == nil {
		t.Fatal("accepted junction")
	}
}

type windowsStagedDelivery struct {
	checks   int
	expected releaseSelection
}

func (*windowsStagedDelivery) closeVerifier() {}

func (d *windowsStagedDelivery) checkVerifier(context.Context, *noticeCache) error {
	return errors.New("unexpected verifier probe")
}
func (d *windowsStagedDelivery) selectRelease(context.Context, string) (releaseSelection, error) {
	return releaseSelection{}, errors.New("unexpected download selection")
}
func (d *windowsStagedDelivery) prepare(context.Context, *noticeCache, releaseSelection, *os.File) (string, error) {
	return "", errors.New("downloaded a prepared release again")
}
func (d *windowsStagedDelivery) stillPublished(_ context.Context, s releaseSelection) error {
	d.checks++
	if s != d.expected {
		return errors.New("wrong release revalidated")
	}
	return nil
}

func windowsStampedFixture(t *testing.T, version, commit string) []byte {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := `package main
import ("fmt"; "io"; "os")
func main() { fmt.Println("running"); _,_ = io.Copy(io.Discard,os.Stdin) }
`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fixture.exe")
	flags := "-X " + stampPrefix + "releaseVersion=" + version + " -X " + stampPrefix + "releaseCommit=" + commit
	cmd := exec.Command("go", "build", "-ldflags", flags, "-o", binary, source)
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native PE: %v %s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWindowsArchiveSharingViolationPreservesUpgrade(t *testing.T) {
	bin, directory := windowsUpdateFixture(t)
	aCommit := strings.Repeat("a", 40)
	commit := strings.Repeat("b", 40)
	b := windowsStampedFixture(t, "1.0.1", commit)
	path := filepath.Join(bin, "openai.exe")
	// Stage a real executable for both versions; keep A an actual running image.
	a := windowsStampedFixture(t, "1.0.0", aCommit)
	if err := os.WriteFile(path, a, 0700); err != nil {
		t.Fatal(err)
	}
	const zone = "[ZoneTransfer]\r\nZoneId=3\r\n"
	if err := os.WriteFile(path+":Zone.Identifier", []byte(zone), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "next"), b, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := openInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	defer installed.close()
	ctx := t.Context()
	cache, err := openManagedUpdateCache(ctx, installed)
	if err != nil {
		t.Fatal(err)
	}
	workerImage, err := pinWindowsWorker(ctx, installed, cache)
	if err != nil {
		t.Fatal(err)
	}
	workerInfo, err := workerImage.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(workerInfo, installed.identity) {
		t.Fatal("worker still owns the mapped public image")
	}
	if !sameWindowsImage(ctx, workerImage, installed) {
		t.Fatal("worker bytes differ from the running CLI")
	}
	workerImage.Close()
	marker, err := os.ReadFile(filepath.Join(directory, windowsWorkerImage) + ":Zone.Identifier")
	if err != nil || string(marker) != zone {
		t.Fatal("private worker lost the download zone", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "next.tmp"), b, 0700); err != nil {
		t.Fatal(err)
	}
	func() {
		current, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer current.Close()
		next, err := os.Open(filepath.Join(directory, "next.tmp"))
		if err != nil {
			t.Fatal(err)
		}
		defer next.Close()
		if err := preservePreparedImageTrust(current, next); err != nil {
			t.Fatal(err)
		}
	}()
	marker, err = os.ReadFile(filepath.Join(directory, "next.tmp") + ":Zone.Identifier")
	if err != nil || string(marker) != zone {
		t.Fatal("staged successor lost the download zone", err)
	}
	if err := os.Remove(filepath.Join(directory, "next.tmp")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	selection := releaseSelection{Version: "1.0.1", Commit: commit, ReleaseID: 3, AssetID: 5, Digest: fmt.Sprintf("%x", sha256.Sum256(b)), Size: int64(len(b)), Updated: now.UTC().Format(time.RFC3339)}
	pending := &preparedUpdate{Release: selection, BinaryHash: selection.Digest, PreviousHash: fmt.Sprintf("%x", sha256.Sum256(a))}
	if err := cache.saveState(updateState{Schema: 1, Pending: pending}); err != nil {
		t.Fatal(err)
	}
	cache.close()

	command := exec.Command(path)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	started := make([]byte, len("running"))
	if _, err := io.ReadFull(stdout, started); err != nil || !bytes.Equal(started, []byte("running")) {
		t.Fatal("old image failed to start", err)
	}
	// A scanner/AV or another CLI consumer can hold a non-delete-sharing handle.
	blocked, err := lockedWindowsImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	// Newer Windows filesystems can replace some occupied destinations. A
	// scanner denying DELETE on the source prevents the atomic rename.
	stagedLock, err := lockedWindowsImage(filepath.Join(directory, "next"))
	if err != nil {
		t.Fatal(err)
	}
	defer stagedLock.Close()
	delivery := &windowsStagedDelivery{expected: selection}
	worker := updateWorker{installation: installed, current: "1.0.0", delivery: delivery, now: now}
	if err = worker.run(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, a) {
		t.Fatal("changed a locked CLI", err)
	}
	cache, err = openManagedUpdateCache(ctx, installed)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := loadUpdateState(cache, now)
	cache.close()
	if err != nil || receipt.Pending == nil || *receipt.Pending != *pending || receipt.Applied != "" {
		t.Fatal("lost or prematurely announced the prepared update", err)
	}
	blocked.Close()
	stagedLock.Close()
	stdin.Close()
	if err = command.Wait(); err != nil {
		t.Fatal("updater interrupted the running CLI", err)
	}
	worker.now = now.Add(2 * time.Minute)
	if err = worker.run(ctx); err != nil {
		t.Fatal(err)
	}
	if delivery.checks != 2 {
		t.Fatalf("expected revalidation on both attempts, got %d", delivery.checks)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, b) {
		t.Fatal("new CLI bytes differ", err)
	}
	if _, err = os.Stat(filepath.Join(directory, "next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staged source remains after replacement", err)
	}
}

func TestWindowsArchivePrivateVerifier(t *testing.T) {
	bin, _ := windowsUpdateFixture(t)
	store, err := openVerifierStore(true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	locked := filepath.Join(store.Name(), ".lock")
	if err := os.WriteFile(locked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(locked, filepath.Join(bin, "foreign-lock")); err != nil {
		t.Fatal(err)
	}
	if safeVerifierStore(store) {
		t.Fatal("accepted a multiply linked verifier lock")
	}
	if err := os.Remove(filepath.Join(bin, "foreign-lock")); err != nil {
		t.Fatal(err)
	}
	if !safeVerifierStore(store) {
		t.Fatal("rejected the repaired private verifier store")
	}
	if err := os.Remove(locked); err != nil {
		t.Fatal(err)
	}

	encoded, binary, pin := windowsVerifierZIP(t)
	calls := 0
	delivery := releaseDelivery{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "github.com" || !strings.HasSuffix(req.URL.Path, "_"+verifierPlatform()+".zip") {
			return nil, errors.New("unexpected verifier URL")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(encoded)), Header: make(http.Header), Request: req}, nil
	})}}
	for i := 0; i < 2; i++ {
		got, err := delivery.installPrivateVerifier(t.Context(), store, verifierPlatform(), "fixture-gh.exe", pin, false)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(got)
		if err != nil || !bytes.Equal(actual, binary) {
			t.Fatal("incorrect private verifier bytes", err)
		}
	}
	zone, err := os.ReadFile(filepath.Join(store.Name(), "fixture-gh.exe") + ":Zone.Identifier")
	if err != nil || !bytes.Contains(zone, []byte("ZoneId=3")) {
		t.Fatal("downloaded private verifier lost its Internet zone", err)
	}
	if calls != 1 {
		t.Fatalf("expected one verifier download across reruns, got %d", calls)
	}
	if err := os.WriteFile(filepath.Join(store.Name(), "fixture-gh.exe"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := delivery.installPrivateVerifier(t.Context(), store, verifierPlatform(), "fixture-gh.exe", pin, false); err == nil {
		t.Fatal("accepted a replaced verifier")
	}
}

func TestWindowsArchiveTrustedSystemVerifier(t *testing.T) {
	bin, _ := windowsUpdateFixture(t)
	path := filepath.Join(bin, "gh.exe")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if !trustedSystemVerifier(path) {
		t.Fatal("rejected privately controlled verifier")
	}
	t.Setenv("PATH", bin)
	if got := systemUpdateVerifier(); !sameWindowsName(got, path) {
		t.Fatalf("failed secure PATH selection: %q", got)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(bin, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if systemUpdateVerifier() != "" {
		t.Fatal("selected a verifier through a replaceable PATH directory")
	}
	// A rejected system verifier must enter the private download path before
	// any executable runs. The intentionally offline transport stops it there.
	privateDir := t.TempDir()
	root, err := os.OpenRoot(privateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	attempted := false
	d := releaseDelivery{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempted = req.URL.Host == "github.com" && strings.Contains(req.URL.Path, "/cli/cli/releases/download/")
		return nil, errors.New("offline fixture")
	})}}
	if err := d.selectVerifier(t.Context(), root.Name(), root, false); err == nil || !attempted {
		t.Fatal("failed to select pinned private fallback", err)
	}
}

func windowsVerifierZIP(t *testing.T) ([]byte, []byte, verifierPin) {
	t.Helper()
	var encoded bytes.Buffer
	writer := zip.NewWriter(&encoded)
	entry, err := writer.Create("bin/gh.exe")
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("synthetic verified gh payload")
	if _, err := entry.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	pin := verifierPin{archive: fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes())), binary: fmt.Sprintf("%x", sha256.Sum256(binary))}
	return encoded.Bytes(), binary, pin
}

// Use the real setup executable's directory and deny every network request.
// Both the archive pin and the executable pin must match before publication.
func TestWindowsArchiveAcquiredVerifier(t *testing.T) {
	bin, _ := windowsUpdateFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENAI_TEST_WINDOWS_ACQUIRED_VERIFIER") != "1" {
		image, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		testExe := filepath.Join(bin, "acquisition-test.exe")
		if err := os.WriteFile(testExe, image, 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), testExe, "-test.run=^TestWindowsArchiveAcquiredVerifier$")
		cmd.Env = append(os.Environ(), "OPENAI_TEST_WINDOWS_ACQUIRED_VERIFIER=1", acquiredVerifierSetting+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private archive handoff: %v\n%s", err, output)
		}
		return
	}
	source := filepath.Join(filepath.Dir(executable), "gh.zip")
	archive, binary, pin := windowsVerifierZIP(t)
	if err := os.WriteFile(source, archive, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source+":Zone.Identifier", []byte(windowsZoneMarker(4)), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(source), "another-link")
	if err := os.Link(source, other); err != nil {
		t.Fatal(err)
	}
	if file, err := acquiredVerifierArchive(); err == nil {
		file.Close()
		t.Fatal("accepted a multiply linked archive")
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	store, err := openVerifierStore(true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	calls := 0
	delivery := releaseDelivery{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network disabled")
	})}}
	for _, wrong := range []verifierPin{{archive: "incorrect", binary: pin.binary}, {archive: pin.archive, binary: "incorrect"}} {
		if _, err := delivery.installPrivateVerifier(t.Context(), store, verifierPlatform(), "local-gh.exe", wrong, true); !errors.Is(err, errVerifierIntegrity) {
			t.Fatal("accepted or misclassified a mismatched private archive", err)
		}
	}
	path, err := delivery.installPrivateVerifier(t.Context(), store, verifierPlatform(), "local-gh.exe", pin, true)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, binary) || calls != 0 {
		t.Fatal("setup did not reuse the authenticated archive", err)
	}
	if zone, _, err := windowsDownloadZone(path); err != nil || zone != 4 {
		t.Fatal("setup lost the supplied archive's restricted zone", err)
	}
	_, err = delivery.installPrivateVerifier(t.Context(), store, verifierPlatform(), "worker-gh.exe", pin, false)
	if !errors.Is(err, errVerifierDownload) || calls != 1 {
		t.Fatal("an ordinary worker consulted the setup handoff", err)
	}
}
