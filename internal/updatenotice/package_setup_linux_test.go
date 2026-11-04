package updatenotice

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxPackageRepeatAfterManagedUpdate(t *testing.T) {
	if !automaticPlatform() {
		t.Skip("package setup requires an ordinary user")
	}
	const version = "1.0.1"
	start := buildUpdateFixture(t, t.TempDir(), "1.0.0")
	binary := buildUpdateFixture(t, t.TempDir(), version)
	installation := updateTestInstallation(t, start)
	home := filepath.Dir(filepath.Dir(filepath.Dir(installation.path)))
	t.Setenv("HOME", home)
	if currentAppliedPackageBinary() {
		t.Fatal("accepted an existing private file without a completed update")
	}
	ctx := context.Background()
	delivery := &syntheticDelivery{selected: syntheticSelection(), content: binary}
	worker := updateWorker{installation: installation, current: "1.0.0", now: time.Now(), delivery: delivery}
	if err := worker.run(ctx); err != nil {
		t.Fatal("prepare package user's first automatic upgrade:", err)
	}
	if currentAppliedPackageBinary() {
		t.Fatal("accepted a pending update before activation")
	}
	if err := worker.run(ctx); err != nil {
		t.Fatal("activate package user's first automatic upgrade:", err)
	}
	if !currentAppliedPackageBinary() {
		t.Fatal("rejected an already-applied managed update")
	}

	digest := fmt.Sprintf("%x", sha256.Sum256(binary))
	state := updateState{Schema: 1, Applied: version, AppliedCommit: syntheticCommit, AppliedHash: digest}
	write := func() {
		t.Helper()
		cache, err := openCache(ctx, installation.stateDirectory())
		if err != nil {
			t.Fatal(err)
		}
		defer cache.close()
		if _, err := loadUpdateState(cache, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := cache.saveState(state); err != nil {
			t.Fatal(err)
		}
	}
	state.AppliedHash = strings.Repeat("0", 64)
	write()
	if currentAppliedPackageBinary() {
		t.Fatal("accepted replaced private bytes")
	}
	state.AppliedHash = digest
	state.AppliedCommit = strings.Repeat("2", 40)
	write()
	if currentAppliedPackageBinary() {
		t.Fatal("accepted a different release commit")
	}
	if err := os.Rename(installation.path, installation.path+".other"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installation.path+".other", installation.path); err != nil {
		t.Fatal(err)
	}
	state.AppliedCommit = syntheticCommit
	write()
	if currentAppliedPackageBinary() {
		t.Fatal("accepted a linked private executable")
	}
}

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
