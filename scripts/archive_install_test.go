//go:build darwin || linux

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const archiveBinary = "#!/bin/sh\n[ \"$1\" = --version ] || exit 2\necho 'openai version 1.2.3'\n"

func TestArchiveInstall(t *testing.T) {
	archive := t.TempDir()
	installer, err := os.ReadFile("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(archive, "install.sh")
	if err := os.WriteFile(script, installer, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(archive, "openai")
	if err := os.WriteFile(binary, []byte(archiveBinary), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(home, path string) *exec.Cmd {
		cmd := exec.Command("sh", script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + path}
		return cmd
	}
	path := os.Getenv("PATH")
	if os.Geteuid() == 0 {
		output, err := run(t.TempDir(), path).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "without sudo") {
			t.Fatalf("root install: err=%v output=%q", err, output)
		}
		return
	}

	t.Run("concurrent installs and repeat", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home with spaces")
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		results := make(chan bool, 8)
		for range 8 {
			go func() { results <- run(home, path).Run() == nil }()
		}
		successes := 0
		for range 8 {
			if <-results {
				successes++
			}
		}
		dest := filepath.Join(home, ".openai", "bin", "openai")
		data, err := os.ReadFile(dest)
		if successes != 1 || err != nil || string(data) != archiveBinary {
			t.Fatalf("successes=%d file=%q err=%v", successes, data, err)
		}
		info, err := os.Stat(dest)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("installed permissions: %v, %v", info, err)
		}
		output, err := run(home, path).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "already exists") {
			t.Fatalf("repeat: err=%v output=%q", err, output)
		}
	})

	t.Run("copy interruption", func(t *testing.T) {
		home := t.TempDir()
		shim := t.TempDir()
		failCopy := "#!/bin/sh\nprintf partial > \"$2\"\nkill -TERM \"$PPID\"\n"
		if err := os.WriteFile(filepath.Join(shim, "cp"), []byte(failCopy), 0700); err != nil {
			t.Fatal(err)
		}
		if err := run(home, shim+":"+path).Run(); err == nil {
			t.Fatal("interrupted install succeeded")
		}
		entries, err := os.ReadDir(filepath.Join(home, ".openai", "bin"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("interruption left files: %v, %v", entries, err)
		}
		output, err := run(home, path).CombinedOutput()
		if err != nil || !strings.Contains(string(output), "Automatic updates require") {
			t.Fatalf("retry: err=%v output=%q", err, output)
		}
	})

	for _, scenario := range []string{"symlink parent", "writable parent", "directory target", "dangling target", "linked target"} {
		t.Run(scenario, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			dedicated := filepath.Join(home, ".openai")
			dest := filepath.Join(dedicated, "bin", "openai")
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if scenario == "symlink parent" {
				err = os.Symlink(outside, dedicated)
			} else if err = os.MkdirAll(filepath.Dir(dest), 0700); err == nil {
				switch scenario {
				case "writable parent":
					err = os.Chmod(dedicated, 0720)
				case "directory target":
					err = os.Mkdir(dest, 0700)
				case "dangling target":
					err = os.Symlink(filepath.Join(outside, "missing"), dest)
				case "linked target":
					err = os.Link(sentinel, dest)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := run(home, path).Run(); err == nil {
				t.Fatal("unsafe install succeeded")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "preserve" {
				t.Fatalf("sentinel=%q err=%v", data, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("external directory changed: %v, %v", entries, err)
			}
		})
	}
	t.Run("linked source", func(t *testing.T) {
		home := t.TempDir()
		if err := os.Link(binary, filepath.Join(archive, "backup")); err != nil {
			t.Fatal(err)
		}
		if err := run(home, path).Run(); err == nil {
			t.Fatal("installed hard-linked source")
		}
		if err := os.Remove(filepath.Join(archive, "backup")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unusable binary", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(binary, []byte("MZ\x00invalid platform\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := run(home, path).Run(); err == nil {
			t.Fatal("installed an unusable binary")
		}
		if err := os.WriteFile(binary, []byte(archiveBinary), 0700); err != nil {
			t.Fatal(err)
		}
		if output, err := run(home, path).CombinedOutput(); err != nil {
			t.Fatalf("corrected archive failed: %v, %s", err, output)
		}
	})
	t.Run("source directory replacement", func(t *testing.T) {
		shim := t.TempDir()
		replace := "#!/bin/sh\nmv \"$ARCHIVE_DIR\" \"$ARCHIVE_DIR.moved\" || exit 1\nmkdir \"$ARCHIVE_DIR\" || exit 1\nprintf wrong > \"$ARCHIVE_DIR/openai\"\nexec /bin/cp \"$@\"\n"
		if err := os.WriteFile(filepath.Join(shim, "cp"), []byte(replace), 0700); err != nil {
			t.Fatal(err)
		}
		cmd := run(t.TempDir(), shim+":"+path)
		cmd.Env = append(cmd.Env, "ARCHIVE_DIR="+archive)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("source changed during copy: %v, %s", err, out)
		}
		// Restore the original directory for the remaining fixture.
		if err := os.Remove(filepath.Join(archive, "openai")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(archive); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(archive+".moved", archive); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("killed after link", func(t *testing.T) {
		home, shim := t.TempDir(), t.TempDir()
		interrupt := "#!/bin/sh\n/bin/ln \"$@\" || exit 1\nkill -KILL \"$PPID\"\n"
		if err := os.WriteFile(filepath.Join(shim, "ln"), []byte(interrupt), 0700); err != nil {
			t.Fatal(err)
		}
		if err := run(home, shim+":"+path).Run(); err == nil {
			t.Fatal("installer survived SIGKILL")
		}
		bin := filepath.Join(home, ".openai", "bin")
		entries, err := os.ReadDir(bin)
		if err != nil || len(entries) != 2 {
			t.Fatalf("expected interrupted stage and installed binary: %v, %v", entries, err)
		}
		if err := run(home, path).Run(); err == nil {
			t.Fatal("retry replaced an installed executable")
		}
		entries, err = os.ReadDir(bin)
		if err != nil || len(entries) != 1 || entries[0].Name() != "openai" {
			t.Fatalf("staging link survived recovery: %v, %v", entries, err)
		}
	})
}
