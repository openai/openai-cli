package updatenotice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxPackageProfileAllowlist(t *testing.T) {
	if !automaticPlatform() {
		t.Skip("package setup requires an ordinary user")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	info, err := os.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_CLI_ARCHIVE_INSTALL", "1")
	operation := func(name string) bool {
		return runLinuxPackageHelper([]string{"openai", "__openai_linux_package_profile", dir, profileID(info) + "/new", name})
	}
	for _, file := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile", ".zshrc"} {
		if !operation(file) {
			t.Fatal("did not handle package profile")
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(data), "$HOME/.openai/bin") {
			t.Fatalf("missing PATH selection for %s: %v", file, err)
		}
	}
	if !operation(".gitconfig") {
		t.Fatal("did not handle invalid profile")
	}
	if _, err := os.Lstat(".gitconfig"); !os.IsNotExist(err) {
		t.Fatal("unexpected write outside allowed profiles")
	}
	t.Setenv("OPENAI_CLI_ARCHIVE_INSTALL", "")
	if operation(".bashrc") {
		t.Fatal("accepted enrollment outside explicit installer mode")
	}
}
