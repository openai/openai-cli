//go:build darwin || linux

package updatenotice

import (
	"os"
	"strings"
	"testing"

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
