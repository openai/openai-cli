//go:build windows

package updatenotice

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/windowsfile"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestWindowsArchiveUserPath(t *testing.T) {
	const dir = `C:\Users\Case test\AppData\Local\OpenAI\CLI\bin`
	const old = `%USERPROFILE%\go\bin;"C:\Preserve;this";C:\Tools;`
	keyName := `Software\OpenAICLI-ArchiveTest-` + rand.Text()
	key, existed, err := registry.CreateKey(registry.CURRENT_USER, keyName, registry.ALL_ACCESS)
	if err != nil || existed {
		t.Fatalf("create disposable user key: existing=%v err=%v", existed, err)
	}
	t.Cleanup(func() {
		key.Close()
		if err := registry.DeleteKey(registry.CURRENT_USER, keyName); err != nil {
			t.Error(err)
		}
	})
	for _, kind := range []uint32{registry.SZ, registry.EXPAND_SZ} {
		set := key.SetStringValue
		if kind == registry.EXPAND_SZ {
			set = key.SetExpandStringValue
		}
		if err := set("Path", old); err != nil {
			t.Fatal(err)
		}
		changed, err := prependWindowsUserPath(key, dir)
		if err != nil || !changed {
			t.Fatalf("first setup: changed=%v err=%v", changed, err)
		}
		value, gotKind, err := key.GetStringValue("Path")
		if err != nil || gotKind != kind || value != dir+";"+old {
			t.Fatalf("registry kind or raw PATH changed: %q %d %v", value, gotKind, err)
		}
		if changed, err = prependWindowsUserPath(key, strings.ToUpper(dir)); err != nil || changed {
			t.Fatalf("repeat setup: changed=%v err=%v", changed, err)
		}
	}
	if err := key.SetBinaryValue("Path", []byte("foreign")); err != nil {
		t.Fatal(err)
	}
	if changed, err := prependWindowsUserPath(key, dir); err == nil || changed {
		t.Fatal("accepted a non-string PATH")
	}
	data, _, err := key.GetBinaryValue("Path")
	if err != nil || string(data) != "foreign" {
		t.Fatal("changed a foreign registry value")
	}
}

func TestWindowsArchiveMachinePathConflict(t *testing.T) {
	managed, other := t.TempDir(), t.TempDir()
	for _, extension := range []string{".exe", ".ps1", ".xyz"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(other, "openai"+extension)
			if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(path) })
			if !windowsMachinePathConflict(other+";"+managed, managed, ".xyz;.exe") {
				t.Fatal("machine command hides the managed executable")
			}
			if windowsMachinePathConflict(managed+";"+other, managed, ".xyz;.exe") {
				t.Fatal("reported a lower-precedence machine command")
			}
		})
	}
}

func TestWindowsArchiveInstallPreservesDownloadZone(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "original.exe"), filepath.Join(dir, "staged.exe")
	for _, name := range []string{source, target} {
		if err := os.WriteFile(name, []byte("archive binary"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyWindowsDownloadZone(source, target); err != nil {
		t.Fatal(err)
	}
	const marking = "[ZoneTransfer]\r\nZoneId=3\r\n"
	if err := os.WriteFile(source+":Zone.Identifier", []byte(marking), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyWindowsDownloadZone(source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target + ":Zone.Identifier")
	if err != nil || string(data) != marking {
		t.Errorf("download zone lost: %q %v", data, err)
	}
	if err := os.Remove(target + ":Zone.Identifier"); err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Repeat([]byte("z"), maxWindowsZoneBytes+1)
	if err := os.WriteFile(source+":Zone.Identifier", oversized, 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyWindowsDownloadZone(source, target); err == nil {
		t.Fatal("accepted excessive security metadata")
	}
	if _, err := os.Open(target + ":Zone.Identifier"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial zone survived a failed copy", err)
	}
	got, err := os.ReadFile(source + ":Zone.Identifier")
	if err != nil || !bytes.Equal(got, oversized) {
		t.Fatal("failed copy modified the original download zone", err)
	}
}

func TestWindowsArchiveSourcePermissions(t *testing.T) {
	source, err := os.CreateTemp(t.TempDir(), "portable-*.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rights  string
		allowed bool
	}{{"FR", true}, {"0x2", false}, {"0x4", false}, {"SD", false}} {
		t.Run(tc.rights, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;" + tc.rights + ";;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(source.Name(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			if allowed := checkWindowsUpdateHandle(source, false, false) == nil; allowed != tc.allowed {
				t.Fatalf("portable source permission: allowed=%v want=%v", allowed, tc.allowed)
			}
			if safeOpenedUpdateFile(source) {
				t.Fatal("private updater file accepted a foreign grant")
			}
		})
	}
}

func TestWindowsArchiveInstallDoesNotAdoptForeignTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "openai.exe")
	if err := os.WriteFile(target, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	image, err := existingWindowsArchive(context.Background(), target, false, strings.Repeat("0", 64), updateState{})
	if err == nil || image.identity != nil {
		t.Fatal("an unmarked existing executable was adopted")
	}
	dir := filepath.Dir(target)
	staged := filepath.Join(dir, ".staged")
	if err := os.WriteFile(staged, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := moveWindowsArchive(staged, target, 0); err == nil {
		t.Fatal("publication overwrote a pre-existing executable")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "foreign" {
		t.Fatal("foreign executable changed")
	}
}

func TestWindowsArchiveInstallMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	handle, err := windowsfile.CreatePrivateDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle.Close()
	ok, err := windowsArchiveEnrolled(dir)
	if ok || err != nil {
		t.Fatal("missing marker qualifies", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := windowsfile.CreatePrivate(root, ".archive-install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("foreign"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if ok, err := windowsArchiveEnrolled(dir); ok || err == nil {
		t.Fatal("foreign marker qualified")
	}
	data, err := root.ReadFile(".archive-install")
	if err != nil || string(data) != "foreign" {
		t.Fatal("foreign marker changed")
	}
	if err := os.WriteFile(filepath.Join(dir, ".archive-install"), []byte(windowsEnrollmentMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := windowsArchiveEnrolled(dir); !ok || err != nil {
		t.Fatal("valid protected marker did not qualify", err)
	}
}
