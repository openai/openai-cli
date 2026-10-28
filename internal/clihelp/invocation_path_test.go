package clihelp

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestHelpInvocationUsesSameExecutableOnPath(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "installed CLI's files")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	executable := filepath.Join(directory, name)
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	if got := Invocation("openai", []string{executable}); got != "openai" {
		t.Fatalf("installed invocation = %q; want openai", got)
	}
	t.Chdir(directory)
	if got := Invocation("openai", []string{"." + string(filepath.Separator) + name}); got != "openai" {
		t.Fatalf("relative installed invocation = %q; want openai", got)
	}
}

func TestHelpInvocationKeepsDifferentExecutable(t *testing.T) {
	root := t.TempDir()
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	for _, directory := range []string{"installed", "local"} {
		path := filepath.Join(root, directory)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, name), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	executable := filepath.Join(root, "local", name)
	t.Setenv("PATH", "")
	want := Invocation("openai", []string{executable})
	if want == "openai" {
		t.Fatal("local invocation must remain usable without PATH installation")
	}
	t.Setenv("PATH", filepath.Join(root, "installed"))
	if got := Invocation("openai", []string{executable}); got != want {
		t.Fatalf("different installed executable changed invocation from %q to %q", want, got)
	}
}

func TestHelpInvocationKeepsLocalPathWithImplicitLookup(t *testing.T) {
	directory := t.TempDir()
	elsewhere := t.TempDir()
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	executable := filepath.Join(directory, name)
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(directory)
	t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",execerrdot=0")
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{elsewhere, ".", string(os.PathListSeparator) + elsewhere} {
		t.Setenv("PATH", path)
		want := "." + string(filepath.Separator) + name
		if got := Invocation("openai", []string{executable}); got != want {
			t.Fatalf("local invocation with PATH=%q = %q; want %q", path, got, want)
		}
	}
	// Absolute PATH installations still shorten when the current directory
	// contains no executable that Windows could discover implicitly.
	t.Chdir(elsewhere)
	t.Setenv("PATH", directory)
	if got := Invocation("openai", []string{executable}); got != "openai" {
		t.Fatalf("absolute PATH invocation = %q; want openai", got)
	}
}

func TestHelpInvocationFollowsInstalledSymlink(t *testing.T) {
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	root := t.TempDir()
	installed := filepath.Join(root, "bin")
	if err := os.Mkdir(installed, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, name)
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(installed, name)
	symlinkHelpInvocationFixture(t, executable, link)
	t.Setenv("PATH", installed)
	if got := Invocation("openai", []string{executable}); got != "openai" {
		t.Fatalf("symlink installed invocation = %q; want openai", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("#!/bin/sh\nexec '"+executable+"' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Invocation("openai", []string{executable}); got == "openai" {
		t.Fatal("a wrapper must not be treated as the invoked executable")
	}
}

func TestHelpInvocationKeepsPowerShellScriptOnPath(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(root, "installed")
	scripts := filepath.Join(root, "scripts")
	for _, directory := range []string{installed, scripts} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	executable := filepath.Join(installed, name)
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	want := Invocation("openai", []string{executable})
	// PowerShell discovers .ps1 files even without a Unix executable bit;
	// exec.LookPath("openai") does not see this competing command.
	if err := os.WriteFile(filepath.Join(scripts, "openai.ps1"), []byte("exit 37\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scripts+string(os.PathListSeparator)+installed)
	if got := Invocation("openai", []string{executable}); got != want {
		t.Fatalf("PowerShell script changed invocation from %q to %q", want, got)
	}
	t.Run("dangling symlink", func(t *testing.T) {
		script := filepath.Join(scripts, "openai.ps1")
		if err := os.Remove(script); err != nil {
			t.Fatal(err)
		}
		symlinkHelpInvocationFixture(t, filepath.Join(scripts, "missing.ps1"), script)
		if got := Invocation("openai", []string{executable}); got != want {
			t.Fatalf("dangling PowerShell script changed invocation from %q to %q", want, got)
		}
	})
}

func symlinkHelpInvocationFixture(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		// ERROR_PRIVILEGE_NOT_HELD permits an explicit local limitation.
		// Required hosted runs must prove the actual symlink behavior.
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) &&
			os.Getenv("OPENAI_CLI_REQUIRE_WINDOWS_SYMLINKS") != "1" {
			t.Skipf("Windows symlink privilege is unavailable: %v", err)
		}
		t.Fatal(err)
	}
}
