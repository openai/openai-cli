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

	clioutput "github.com/openai/openai-cli/internal/output"
	filepermstest "github.com/openai/openai-cli/internal/testutil/fileperms"
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

func TestWindowsDownloadPrivateCreation(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := clioutput.CreatePrivateDownloadFile(root, "download.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	filepermstest.AssertPrivate(t, file)
	if _, err := file.Write([]byte("synthetic")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := clioutput.CreatePrivateDownloadFile(root, "download.bin"); !errors.Is(err, os.ErrExist) || duplicate != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		t.Fatalf("duplicate creation must preserve os.ErrExist: %v", err)
	}
	for _, name := range []string{"../outside", `sub\outside`, `C:\outside`} {
		if escaped, err := clioutput.CreatePrivateDownloadFile(root, name); err == nil || err.Error() != "download staging requires a local filename" {
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
