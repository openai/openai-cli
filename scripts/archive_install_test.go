//go:build darwin || linux

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	t.Run("GitHub CLI requirements", func(t *testing.T) {
		t.Cleanup(func() {
			if err := os.WriteFile(binary, []byte(archiveBinary), 0700); err != nil {
				t.Error(err)
			}
		})
		t.Run("verifier selected after setup", func(t *testing.T) {
			home := t.TempDir()
			managed := filepath.Join(home, ".openai", "bin")
			if err := os.MkdirAll(managed, 0700); err != nil {
				t.Fatal(err)
			}
			oldPath := t.TempDir()
			for name, body := range map[string]string{
				filepath.Join(oldPath, "gh"): "#!/bin/sh\necho ready\n",
				filepath.Join(managed, "gh"): "#!/bin/sh\necho unavailable\n",
				binary:                       "#!/bin/sh\ncase $1 in --version) echo 'openai version 1.2.3';; __openai_archive_verifier) gh;; esac\n",
			} {
				if err := os.WriteFile(name, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := run(home, oldPath+":"+path)
			cmd.Args = append(cmd.Args, "--require-gh")
			if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "verification is unavailable") {
				t.Fatalf("installer accepted the previous PATH: %v: %s", err, output)
			}
			if _, err := os.Lstat(filepath.Join(managed, "openai")); !os.IsNotExist(err) {
				t.Fatalf("installed before verification: %v", err)
			}
		})
		for _, tc := range []struct{ readiness, hint string }{
			{"missing", "Install GitHub CLI"},
			{"unavailable", "Update GitHub CLI"},
			{"ready", "GitHub CLI supports update verification"},
		} {
			t.Run(tc.readiness, func(t *testing.T) {
				staged := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'openai version 1.2.3';;\n__openai_archive_verifier) echo " + tc.readiness + ";;\n*) exit 9;;\nesac\n"
				if err := os.WriteFile(binary, []byte(staged), 0700); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				strict := run(home, path)
				strict.Args = append(strict.Args, "--require-gh")
				output, err := strict.CombinedOutput()
				ready := tc.readiness == "ready"
				if (err == nil) != ready || !strings.Contains(string(output), tc.hint) {
					t.Fatalf("strict installer: %v: %s", err, output)
				}
				target := filepath.Join(home, ".openai", "bin", "openai")
				if !ready {
					if _, err := os.Lstat(target); !os.IsNotExist(err) {
						t.Fatalf("strict installer published %s: %v", target, err)
					}
					output, err = run(home, path).CombinedOutput()
					if err != nil || !strings.Contains(string(output), tc.hint) || !strings.Contains(string(output), "After fixing gh") {
						t.Fatalf("ordinary install: %v: %s", err, output)
					}
				}
				if got, err := exec.Command(target, "--version").CombinedOutput(); err != nil || string(got) != "openai version 1.2.3\n" {
					t.Fatalf("installed CLI: %v: %s", err, got)
				}
			})
		}
	})

	if runtime.GOOS == "darwin" {

		for _, tc := range []struct {
			target string
			option string
			acl    string
			safe   bool
		}{
			{"archive", "+a", "allow delete_child", false},
			{"home", "+a", "allow add_file", false},
			{"dedicated", "+a", "allow add_subdirectory,directory_inherit,only_inherit", false},
			{"bin", "+a", "allow writesecurity", false},
			{"source", "+a", "allow chown", false},
			{"source", "+ai", "allow write", false},
			{"source", "+a", "deny readsecurity", false},
			{"home", "+a", "deny delete", true},
			{"bin", "+a", "allow list,search,readattr,file_inherit,directory_inherit", true},
			{"source", "+ai", "allow read,execute,readattr,readextattr,readsecurity", true},
		} {
			t.Run("macOS ACL/"+tc.target+" "+tc.acl, func(t *testing.T) {
				home := t.TempDir()
				dedicated := filepath.Join(home, ".openai")
				bin := filepath.Join(dedicated, "bin")
				if err := os.MkdirAll(bin, 0700); err != nil {
					t.Fatal(err)
				}
				target := map[string]string{"archive": archive, "source": binary, "home": home, "dedicated": dedicated, "bin": bin}[tc.target]
				if output, err := exec.Command("/bin/chmod", tc.option, "group:everyone "+tc.acl, target).CombinedOutput(); err != nil {
					t.Fatalf("add ACL: %v: %s", err, output)
				}
				t.Cleanup(func() {
					if output, err := exec.Command("/bin/chmod", "-N", target).CombinedOutput(); err != nil {
						t.Errorf("remove ACL: %v: %s", err, output)
					}
				})
				output, err := run(home, path).CombinedOutput()
				if (err == nil) != tc.safe {
					t.Fatalf("install: %v: %s", err, output)
				}
				_, err = os.Lstat(filepath.Join(bin, "openai"))
				if tc.safe && err != nil || !tc.safe && !os.IsNotExist(err) {
					t.Fatalf("installed file: %v", err)
				}
			})
		}
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
		if err != nil || !strings.Contains(string(output), "Installed OpenAI CLI 1.2.3") {
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
