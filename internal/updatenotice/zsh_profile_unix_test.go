//go:build darwin || linux

package updatenotice

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/autocomplete"

	"golang.org/x/sys/unix"
)

func TestArchiveZshProfile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("setup requires an ordinary user")
	}
	const selection = `export PATH="$HOME/.openai/bin:$PATH"`
	selected := t.TempDir()
	t.Chdir(selected)
	// Setup requires directory search and write access. It need not list the directory.
	if err := os.Chmod(selected, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(selected, 0700); err != nil {
			t.Error(err)
		}
	})
	id := func(path string) string {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return profileID(info)
	}
	dir := id(".") + "/"
	if enableZshPath(t.TempDir(), dir+"new", false) {
		t.Fatal("accepted a replaced profile directory")
	}
	if enableZshPath(selected, dir+"new", true) {
		t.Fatal("created an absent .zlogin")
	}
	if !enableZshPath(selected, dir+"new", false) {
		t.Fatal("failed to create the selected profile")
	}
	identity := dir + id(".zshrc")
	before := selection + "\nexport PATH=/older/bin:$PATH\n"
	if err := os.WriteFile(".zshrc", []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if !enableZshPath(selected, identity, false) || !enableZshPath(selected, identity, false) {
		t.Fatal("failed to enroll or repeat enrollment")
	}
	data, err := os.ReadFile(".zshrc")
	if err != nil || string(data) != before+"\n"+selection+"\n" {
		t.Fatalf("unexpected profile tail: %q (%v)", data, err)
	}
	// Keep the original inode alive while replacing its name.
	if err := os.Rename(".zshrc", "original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("replacement", []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, replace := range []func() error{
		func() error { return os.Symlink("replacement", ".zshrc") },
		func() error { return os.Link("replacement", ".zshrc") },
		func() error { return unix.Mkfifo(".zshrc", 0600) },
		func() error { return os.WriteFile(".zshrc", []byte("different"), 0600) },
	} {
		if err := replace(); err != nil {
			t.Fatal(err)
		}
		if enableZshPath(selected, identity, false) || enableZshPath(selected, dir+"new", false) {
			t.Fatal("accepted a replacement")
		}
		if err := os.Remove(".zshrc"); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile("replacement")
	if err != nil || strings.Contains(string(data), selection) {
		t.Fatal("modified a replacement via a link")
	}
}

func TestArchivePathAndInsertionStayOrderedOnReinstall(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("setup requires an ordinary user")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Chdir(home)
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(profile, []byte("# personal settings\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options := autocomplete.SuggestionInstallation{
		Shell: autocomplete.CompletionStyleZsh, Directory: filepath.Join(home, "shell"), Profile: profile,
		Executable: filepath.Join(home, ".openai", "bin", "openai"), AfterShellSetup: true,
	}
	enroll := func() []byte {
		t.Helper()
		dir, err := os.Stat(home)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.Stat(profile)
		if err != nil {
			t.Fatal(err)
		}
		if !enableZshPath(home, profileID(dir)+"/"+profileID(file), false) {
			t.Fatal("PATH enrollment failed")
		}
		if _, err := autocomplete.InstallSuggestions(context.Background(), options); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := enroll()
	if next := enroll(); !bytes.Equal(first, next) {
		t.Fatal("repeat enrollment changed an intact profile")
	}
	// The user replaced the old PATH setup after the previous installation.
	stale := bytes.ReplaceAll(first, []byte(`export PATH="$HOME/.openai/bin:$PATH"`), []byte(`export PATH="/stale:$PATH"`))
	stale = append(stale, []byte("export PATH=/later:$PATH\n")...)
	if err := os.WriteFile(profile, stale, 0600); err != nil {
		t.Fatal(err)
	}
	repaired := enroll()
	if bytes.Count(repaired, []byte("# >>> openai command insertion")) != 1 ||
		bytes.Index(repaired, []byte("# >>> openai command insertion")) < bytes.LastIndex(repaired, []byte(`export PATH="$HOME/.openai/bin:$PATH"`)) ||
		!bytes.Contains(repaired, []byte("export PATH=/later:$PATH")) {
		t.Fatal("loader did not follow the restored PATH")
	}
	if next := enroll(); !bytes.Equal(next, repaired) {
		t.Fatal("repeat enrollment changed the repaired profile")
	}
}
