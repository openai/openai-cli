//go:build windows

package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsDownloadAutomaticConsole(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, helper, "-test.run=^TestShellFileAutomaticConsoleHelper$", "--", "console")
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	process.WaitDelay = time.Second
	output, err := process.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "automatic console save passed") || !strings.Contains(string(output), "automatic console source-close passed") {
		t.Fatalf("native automatic console save: %v\n%s", err, output)
	}
}

func TestShellFileAutomaticConsoleHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	if !slices.Equal(os.Args[separator+1:], []string{"console"}) {
		t.Fatal("invalid automatic console helper arguments")
	}
	// GetConsoleMode requires GENERIC_READ, including for an output buffer.
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = console
	t.Cleanup(func() {
		os.Stdout = original
		if err := console.Close(); err != nil {
			t.Error(err)
		}
	})
	if !isTerminal(os.Stdout) {
		t.Fatal("CONOUT$ is not an actual Windows console")
	}
	TestWriteBinaryResponseAutomaticTerminalOutput(t)
	TestAutomaticDownloadSourceCloseFailure(t)
	if t.Skipped() {
		t.Fatal("automatic console checks did not execute")
	}
	if !t.Failed() {
		if _, err := io.WriteString(original, "automatic console save passed\nautomatic console source-close passed\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPrivateWindowsDownload(t *testing.T, file *os.File) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("download DACL must be protected: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("download requires an explicit DACL: %v", err)
	}
	var caller, system, administrators bool
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Mask&fileAllAccess != fileAllAccess {
			t.Fatal("download access rule is not an explicit full-access grant")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			caller = true
		case sid.IsWellKnown(windows.WinLocalSystemSid):
			system = true
		case sid.IsWellKnown(windows.WinBuiltinAdministratorsSid):
			administrators = true
		default:
			t.Fatal("download grants access to an unexpected principal")
		}
	}
	// The normal runner has a distinct user SID. SYSTEM execution can combine these identities.
	system = system || user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) && caller
	administrators = administrators || user.User.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && caller
	if !caller || !system || !administrators {
		t.Fatal("download is missing an intended access grant")
	}
}

func TestWindowsDownloadPrivateCreation(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := createPrivateDownloadFile(root, "download.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assertPrivateWindowsDownload(t, file)
	if _, err := file.Write([]byte("synthetic")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := createPrivateDownloadFile(root, "download.bin"); !errors.Is(err, os.ErrExist) || duplicate != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		t.Fatalf("duplicate creation must preserve os.ErrExist: %v", err)
	}
	for _, name := range []string{"../outside", `sub\outside`, `C:\outside`} {
		if escaped, err := createPrivateDownloadFile(root, name); err == nil || err.Error() != "download staging requires a local filename" {
			if escaped != nil {
				escaped.Close()
			}
			t.Fatalf("nonlocal name %q diagnostic=%v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(directory, "download.bin")); err != nil || string(data) != "synthetic" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

func TestWindowsDownloadPrivatePublication(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "hardlink", true: "exclusive copy"}[fallback], func(t *testing.T) {
			directory := t.TempDir()
			stage, err := newDownloadStage(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer requireNoDownloadStages(t, directory)
			defer func() {
				if err := stage.cleanup(); err != nil {
					t.Error(err)
				}
			}()
			assertPrivateWindowsDownload(t, stage.file)
			payload := []byte{0, 255, 128, 's', '\n'}
			if _, err := stage.file.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := stage.closeWriter(); err != nil {
				t.Fatal(err)
			}
			link := stage.root.Link
			if fallback {
				link = func(string, string) error { return errors.New("synthetic unsupported hardlink") }
			}
			saved, incomplete, err := publishNewDownload(context.Background(), stage, "output.bin", link)
			if err != nil || incomplete || saved == nil {
				t.Fatalf("publication=%v incomplete=%t error=%v", saved, incomplete, err)
			}
			file, err := os.Open(filepath.Join(directory, "output.bin"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			assertPrivateWindowsDownload(t, file)
			data, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("published bytes=%x error=%v", data, err)
			}
		})
	}
}

func TestWindowsDownloadPreservesExistingDACL(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "source failure"}[failure], func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "existing.bin")
			if err := os.WriteFile(destination, []byte("GOOD"), 0600); err != nil {
				t.Fatal(err)
			}
			user, err := windows.GetCurrentProcessToken().GetTokenUser()
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;SY)(A;;FR;;;BA)")
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(destination, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
				t.Fatal(err)
			}
			before, err := windows.GetNamedSecurityInfo(destination, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte{0, 255, 'n', 'e', 'w'}
			var source io.Reader = bytes.NewReader(payload)
			if failure {
				source = &errorAfterDownloadReader{Reader: source, err: errors.New("synthetic source failure")}
			}
			message, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(source)}, io.Discard, destination)
			if (err != nil) != failure || (message == "") != failure {
				t.Fatalf("save message=%q error=%v", message, err)
			}
			after, err := windows.GetNamedSecurityInfo(destination, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			current, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() || !os.SameFile(original, current) {
				t.Fatal("save changed the existing identity or DACL")
			}
			if failure {
				payload = []byte("GOOD")
			}
			if data, err := os.ReadFile(destination); err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("destination bytes=%x error=%v", data, err)
			}
			requireNoDownloadStages(t, directory)
		})
	}
}
