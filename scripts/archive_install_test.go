//go:build darwin || linux

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/updatenotice"
)

const archiveBinary = "#!/bin/sh\n[ \"$1\" = --version ] || exit 2\necho 'openai version 1.2.3'\n"

// Go's temporary test directories can inherit a group-writable developer umask.
func privateInstallDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestArchiveInstall(t *testing.T) {
	archive := privateInstallDirectory(t)
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
		output, err := run(privateInstallDirectory(t), path).CombinedOutput()
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
			home := privateInstallDirectory(t)
			managed := filepath.Join(home, ".openai", "bin")
			if err := os.MkdirAll(managed, 0700); err != nil {
				t.Fatal(err)
			}
			oldPath := privateInstallDirectory(t)
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
				home := privateInstallDirectory(t)
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

	t.Run("Homebrew opt in", testHomebrewOptIn)

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
				home := privateInstallDirectory(t)
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
		home := filepath.Join(privateInstallDirectory(t), "home with spaces")
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
		home := privateInstallDirectory(t)
		shim := privateInstallDirectory(t)
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
			home, outside := privateInstallDirectory(t), privateInstallDirectory(t)
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
		home := privateInstallDirectory(t)
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
		home := privateInstallDirectory(t)
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
		shim := privateInstallDirectory(t)
		replace := "#!/bin/sh\nmv \"$ARCHIVE_DIR\" \"$ARCHIVE_DIR.moved\" || exit 1\nmkdir \"$ARCHIVE_DIR\" || exit 1\nprintf wrong > \"$ARCHIVE_DIR/openai\"\nexec /bin/cp \"$@\"\n"
		if err := os.WriteFile(filepath.Join(shim, "cp"), []byte(replace), 0700); err != nil {
			t.Fatal(err)
		}
		cmd := run(privateInstallDirectory(t), shim+":"+path)
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
		home, shim := privateInstallDirectory(t), privateInstallDirectory(t)
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

func testHomebrewOptIn(t *testing.T) {
	prefix := privateInstallDirectory(t)
	source := filepath.Join(prefix, "Caskroom", "openai", "1.2.3")
	bin := filepath.Join(prefix, "bin")
	for _, dir := range []string{source, bin} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	installer, err := os.ReadFile("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	const name = "openai-enable-updates.zsh"
	selection, err := os.ReadFile("../" + name)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(bin, name)
	if err := os.Symlink("../Caskroom/openai/1.2.3/"+name, helper); err != nil {
		t.Fatal(err)
	}
	// The fixture supplies a synthetic release version and verifier. Its profile
	// operation executes the production handler inside this native test binary.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "'" + strings.ReplaceAll(self, "'", "'\\''") + "'"
	ready := []byte(`#!/bin/sh
case $1 in
    --version) echo 'openai version 1.2.3' ;;
    __openai_homebrew_verifier)
        echo probe >> "$HOME/formula.log"
        if [ -x "$2/bin/gh" ]; then "$2/bin/gh"; else echo missing; fi ;;
    __openai_archive_verifier)
        echo probe >> "$HOME/verifier.log"
        command -v gh >/dev/null && gh || echo missing ;;
    __openai_archive_zsh_profile|__openai_zsh_login_profile) exec ` + command + ` -test.run '^TestArchiveProfileProcess$' -- "$@" ;;
esac
`)
	for path, data := range map[string][]byte{
		filepath.Join(source, name):         selection,
		filepath.Join(source, "install.sh"): installer,
		filepath.Join(source, "openai"):     ready,
		filepath.Join(bin, "openai"):        []byte("#!/bin/sh\nexit 9\n"),
	} {
		if err := os.WriteFile(path, data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(home string, args ...string) (string, error) {
		t.Helper()
		c := exec.Command("sh", append([]string{filepath.Join(source, "install.sh"), "--require-gh"}, args...)...)
		c.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
		out, err := c.CombinedOutput()
		return string(out), err
	}
	home := privateInstallDirectory(t)
	profileDir := filepath.Join(home, "my zsh")
	profile := filepath.Join(profileDir, ".zshrc")
	if err := os.Mkdir(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("# personal settings"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(home, "--zsh", profileDir); err == nil || !strings.Contains(out, "verification is unavailable") {
		t.Fatalf("missing verifier: %v %s", err, out)
	}
	if data, err := os.ReadFile(profile); err != nil || string(data) != "# personal settings" {
		t.Fatalf("profile changed after missing verifier: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if out, err := run(home, "--zsh", profileDir); err != nil {
			t.Fatalf("enable attempt %d: %v %s", i, err, out)
		}
	}
	if data, err := os.ReadFile(profile); err != nil || string(data) != "# personal settings\nexport PATH=\"$HOME/.openai/bin:$PATH\"\n" {
		t.Fatalf("profile after repeat: %q %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(profileDir, ".zlogin")); !os.IsNotExist(err) {
		t.Fatalf("created an absent .zlogin: %v", err)
	}
	target := filepath.Join(home, ".openai", "bin", "openai")
	if data, err := os.ReadFile(target); err != nil || string(data) != string(ready) {
		t.Fatalf("wrong executable: %q %v", data, err)
	}
	if zsh, err := exec.LookPath("zsh"); err == nil {
		c := exec.Command(zsh, "-d", "-i", "-c", "command -v openai")
		c.Env = []string{"HOME=" + home, "ZDOTDIR=" + profileDir, "PATH=" + bin + ":/usr/bin:/bin"}
		if out, err := c.Output(); err != nil || strings.TrimSpace(string(out)) != target {
			t.Fatalf("fresh zsh selects %q: %v", out, err)
		}
	}
	t.Run("source in the caller", func(t *testing.T) {
		zsh, err := exec.LookPath("zsh")
		if err != nil {
			t.Skip("zsh unavailable")
		}
		brew := filepath.Join(bin, "brew")
		brewScript := `#!/bin/sh
prefix=$(dirname "$(dirname "$0")")
case $1 in
    --caskroom) echo "$prefix/Caskroom/openai" ;;
    --prefix) echo "$prefix" ;;
    update)
        [ "$#" -eq 1 ] || exit 71
        echo update >> "$HOME/brew.log"
        [ "${BREW_FAIL:-}" != update ] || exit 39
        if [ "${BREW_FAIL:-}" = old-tap ]; then version=1.0.0; else version=1.2.3; fi
        echo "$version" > "$prefix/tap-version" ;;
    install)
        if [ "$2" = --cask ]; then
            [ "$#" -eq 3 ] && [ "$3" = openai/tools/openai ] || exit 71
            echo cask >> "$HOME/brew.log"
            [ "${BREW_FAIL:-}" != install ] || exit 40
            version=$(cat "$prefix/tap-version") || exit 71
            release="$prefix/Caskroom/openai/$version"
            mkdir -p "$release" && cp "$BREW_RELEASE_FIXTURE/"* "$release/" || exit 71
            for name in openai openai-enable-updates.zsh; do
                [ -f "$release/$name" ] || continue
                ln -sf "$release/$name" "$prefix/bin/$name" || exit 71
            done
            exit 0
        fi
        [ "$2" = gh ] || exit 71
        echo gh >> "$HOME/brew.log"
        [ "${BREW_RC:-0}" -eq 0 ] || exit "$BREW_RC"
        printf '%s\n' '#!/bin/sh' 'echo ready' > "$prefix/bin/gh"
        chmod 755 "$prefix/bin/gh" ;;
    *) exit 72 ;;
esac
`
		if err := os.WriteFile(brew, []byte(brewScript), 0755); err != nil {
			t.Fatal(err)
		}
		t.Run("README installation", func(t *testing.T) {
			testHomebrewReadmeInstall(t, zsh, source, brewScript)
		})
		t.Run("login profile", func(t *testing.T) {
			testZshLoginSelection(t, zsh, bin)
		})
		for _, kind := range []string{"normal", "hashed", "external gh", "missing gh", "old gh", "shadowed gh", "alias", "function", "failed brew", "missing sibling", "linked sibling", "wrong release link"} {
			t.Run(kind, func(t *testing.T) {
				home := privateInstallDirectory(t)
				switch kind {
				case "missing gh", "old gh", "failed brew", "external gh":
					gh := filepath.Join(bin, "gh")
					if err := os.Rename(gh, gh+".saved"); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.Rename(gh+".saved", gh); err != nil {
							t.Error(err)
						}
					})
					if kind == "old gh" {
						if err := os.WriteFile(gh, []byte("#!/bin/sh\necho unavailable\n"), 0755); err != nil {
							t.Fatal(err)
						}
					}
				case "missing sibling", "linked sibling":
					path := filepath.Join(source, "install.sh")
					if err := os.Rename(path, path+".saved"); err != nil {
						t.Fatal(err)
					}
					if kind == "linked sibling" {
						if err := os.Symlink(path+".saved", path); err != nil {
							t.Fatal(err)
						}
					}
					t.Cleanup(func() {
						if err := os.Rename(path+".saved", path); err != nil {
							t.Error(err)
						}
					})
				case "wrong release link":
					other := privateInstallDirectory(t)
					for _, f := range []string{name, "install.sh"} {
						data, err := os.ReadFile(filepath.Join(source, f))
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(other, f), data, 0755); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Rename(helper, helper+".saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(other, name), helper); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.Rename(helper+".saved", helper); err != nil {
							t.Error(err)
						}
					})
				}
				shell := `
setopt noclobber
before=$PWD
if [[ $KIND == hashed ]]; then
    export PATH="$HOME/.openai/bin:$PATH"
    hash openai="$HOME/stale"
fi
initial=$PATH
hash keep="$HOME/keep"
entry=preserve
ZDOTDIR="$HOME/my zsh"
mkdir "$ZDOTDIR" || exit 91
case $KIND in
    alias) alias openai=false ;;
    function) openai() { return 81; } ;;
    'failed brew') export BREW_RC=39 ;;
    'external gh')
        mkdir -p "$HOME/external" || exit 80
        printf '%s\n' '#!/bin/sh' 'echo ready' > "$HOME/external/gh"
        chmod 700 "$HOME/external/gh" || exit 80
        export PATH="$HOME/external:$PATH" ;;
    'shadowed gh')
        mkdir -p "$HOME/.openai/bin" || exit 80
        printf '%s\n' '#!/bin/sh' 'echo unavailable' > "$HOME/.openai/bin/gh"
        chmod 700 "$HOME/.openai/bin/gh" || exit 80 ;;
esac
source "$(brew --prefix)/bin/openai-enable-updates.zsh"
rc=$?
[[ $before == "$PWD" && $entry == preserve && -o noclobber ]] || exit 92
[[ $commands[keep] == "$HOME/keep" ]] || [[ $initial != "$PATH" ]] || exit 98
if [[ $KIND = (normal|hashed|'external gh'|'missing gh'|'old gh'|'shadowed gh') ]]; then
    (( rc == 0 )) && [[ $(whence openai) == "$HOME/.openai/bin/openai" ]] || exit 93
    selected=$PATH
    source "$(brew --prefix)/bin/openai-enable-updates.zsh" || exit 94
    [[ $selected == "$PATH" ]] || exit 95
    if [[ $KIND = ('missing gh'|'old gh') ]]; then
        [[ $(cat "$HOME/brew.log") = gh && $(wc -l < "$HOME/formula.log") -eq 3 && $(wc -l < "$HOME/verifier.log") -eq 1 ]] || exit 85
    elif [[ $KIND = 'external gh' ]]; then
        [[ ! -e "$HOME/brew.log" && $(wc -l < "$HOME/verifier.log") -eq 2 ]] || exit 86
    else
        [[ ! -e "$HOME/brew.log" && ! -e "$HOME/verifier.log" && $(wc -l < "$HOME/formula.log") -eq 2 ]] || exit 86
        if [[ $KIND = 'shadowed gh' ]]; then
            [[ $(whence gh) = "$HOME/.openai/bin/gh" && $(gh) = unavailable ]] || exit 88
        fi
    fi
else
    (( rc != 0 )) && [[ $initial == "$PATH" && ! -e "$ZDOTDIR/.zshrc" && ! -e "$HOME/.openai/bin/openai" ]] || exit 96
    if [[ $KIND = 'failed brew' ]]; then
        [[ $rc = 39 && $(cat "$HOME/brew.log") = gh ]] || exit 87
    else
        [[ ! -e "$HOME/brew.log" ]] || exit 97
    fi
fi
`
				cmd := exec.Command(zsh, "-f", "-c", shell)
				cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "KIND=" + kind}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("caller: %v: %s", err, out)
				}
			})
		}
	})

	for _, kind := range []string{"linked profile", "hard-linked profile", "shared profile", "linked directory", "occupied destination"} {
		t.Run(kind, func(t *testing.T) {
			home := privateInstallDirectory(t)
			sentinel := filepath.Join(privateInstallDirectory(t), "original")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := home
			profile := filepath.Join(home, ".zshrc")
			dest := filepath.Join(home, ".openai", "bin", "openai")
			var err error
			switch kind {
			case "linked profile":
				err = os.Symlink(sentinel, profile)
			case "hard-linked profile":
				err = os.Link(sentinel, profile)
			case "shared profile":
				if err = os.WriteFile(profile, []byte("preserve"), 0600); err == nil {
					err = os.Chmod(profile, 0660)
				}
			case "linked directory":
				dir = filepath.Join(home, "zsh")
				err = os.Symlink(filepath.Dir(sentinel), dir)
			case "occupied destination":
				if err = os.MkdirAll(filepath.Dir(dest), 0700); err == nil {
					err = os.WriteFile(dest, []byte("preserve"), 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if out, err := run(home, "--zsh", dir); err == nil {
				t.Fatalf("unexpected success: %s", out)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
				t.Fatalf("sentinel changed: %q %v", data, err)
			}
			if kind == "occupied destination" {
				if data, err := os.ReadFile(dest); err != nil || string(data) != "preserve" {
					t.Fatalf("changed occupied destination: %q %v", data, err)
				}
				if _, err := os.Lstat(profile); !os.IsNotExist(err) {
					t.Fatalf("profile appeared after failure: %v", err)
				}
			} else if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("published with unsafe profile: %v", err)
			}
		})
	}
	if err := os.Chmod(source, 0770); err != nil {
		t.Fatal(err)
	}
	if out, err := run(privateInstallDirectory(t)); err == nil || !strings.Contains(out, "only you can modify") {
		t.Fatalf("group-writable source: %v %s", err, out)
	}
}

func testZshLoginSelection(t *testing.T, zsh, bin string) {
	for _, kind := range []string{"regular", "symlink", "readonly path", "readonly PATH", "early return"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(privateInstallDirectory(t), "home [old]")
			settings := filepath.Join(home, "my settings")
			goBin := filepath.Join(home, "go", "bin")
			for _, dir := range []string{settings, goBin} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(goBin, "openai"), []byte("#!/bin/sh\necho old\n"), 0700); err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(settings, ".zlogin")
			personal := profile
			if kind == "symlink" {
				personal = filepath.Join(privateInstallDirectory(t), "linked profile")
				if err := os.Symlink(personal, profile); err != nil {
					t.Fatal(err)
				}
			}
			content := "echo read >> \"$HOME/login-runs\"\nexport PATH=\"$HOME/go/bin::$PATH\""
			switch kind {
			case "readonly path", "readonly PATH":
				content += "\n" + kind
			case "early return":
				content += "\nreturn"
			}
			if err := os.WriteFile(personal, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			path := goBin + ":" + bin + ":/usr/bin:/bin"
			run := func(flags, script string) string {
				t.Helper()
				cmd := exec.Command(zsh, flags, script)
				cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + settings, "PATH=" + path}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("zsh %s: %v: %s", flags, err, out)
				}
				return string(out)
			}
			var previous string
			for attempt := range 2 {
				out := run("-dfic", `source "$(brew --prefix)/bin/openai-enable-updates.zsh" &&
[[ $(command -v openai) == "$HOME/.openai/bin/openai" && $(openai --version) == 'openai version 1.2.3' ]]`)
				warned := strings.Contains(out, "could not safely configure") && strings.Contains(out, `"$HOME/.openai/bin/openai" directly`)
				if warned != (kind == "symlink") {
					t.Fatalf("unexpected login profile advice: %s", out)
				}
				data, err := os.ReadFile(personal)
				if err != nil || !strings.HasPrefix(string(data), content) || kind == "symlink" && string(data) != content {
					t.Fatalf("personal profile after setup: %q, %v", data, err)
				}
				if attempt > 0 && string(data) != previous {
					t.Fatal("repeat setup changed the profile")
				}
				previous = string(data)
			}
			if _, err := os.Stat(filepath.Join(home, "login-runs")); !os.IsNotExist(err) {
				t.Fatalf("setup executed the login profile: %v", err)
			}
			managed := filepath.Join(home, ".openai", "bin")
			for _, flags := range []string{"-dic", "-dlic", "-dlc"} {
				wantPath, version := managed+":"+path, "openai version 1.2.3"
				switch flags {
				case "-dlic":
					if kind == "regular" {
						wantPath = managed + ":" + goBin + "::" + path
					} else {
						wantPath, version = goBin+"::"+managed+":"+path, "old"
					}
				case "-dlc":
					wantPath, version = goBin+"::"+path, "old"
				}
				out := run(flags, `openai --version && print -r -- "$PATH"`)
				if out != version+"\n"+wantPath+"\n" {
					t.Fatalf("zsh %s selection: %q, want %q on PATH %q", flags, out, version, wantPath)
				}
			}
		})
	}
}

func testHomebrewReadmeInstall(t *testing.T, zsh, release, brewScript string) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, start, ok := strings.Cut(string(readme), "\n```zsh\n")
	block, rest, closed := strings.Cut(start, "\n```")
	if !ok || !closed {
		t.Fatal("missing zsh installation block")
	}
	_, start, ok = strings.Cut(rest, "\n```zsh\n")
	recovery, _, closed := strings.Cut(start, "\n```")
	if !ok || !closed {
		t.Fatal("missing zsh recovery block")
	}
	for _, tc := range []struct {
		name, failure, calls string
		exitCode             int
	}{
		{"fresh without GitHub CLI", "", "update\ncask\ngh\n", 0},
		{"old cask and earlier Go binary", "", "update\ncask\n", 0},
		{"failed update", "update", "update\n", 39},
		{"failed install", "install", "update\ncask\n", 40},
		{"tap still offers a cask without setup", "old-tap", "update\ncask\n", -1}, // zsh chooses the missing-file status.
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := privateInstallDirectory(t)
			prefix := filepath.Join(home, "brew prefix")
			bin := filepath.Join(prefix, "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "brew"), []byte(brewScript), 0755); err != nil {
				t.Fatal(err)
			}
			if tc.name != "fresh without GitHub CLI" {
				if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			available := release
			if tc.failure == "old-tap" {
				available = privateInstallDirectory(t)
				const oldCaskCLI = "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = --help ] || exit 2\necho 'Usage from cask 1.0.0: openai [command]'\n"
				if err := os.WriteFile(filepath.Join(available, "openai"), []byte(oldCaskCLI), 0755); err != nil {
					t.Fatal(err)
				}
			}
			profile := "# personal settings\n"
			preflight := ""
			if tc.name == "old cask and earlier Go binary" || tc.failure == "old-tap" {
				goBin := filepath.Join(home, "go", "bin")
				oldCask := filepath.Join(prefix, "Caskroom", "openai", "1.0.0")
				for _, dir := range []string{goBin, oldCask} {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "openai"), []byte("#!/bin/sh\necho old\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(filepath.Join(oldCask, "openai"), filepath.Join(bin, "openai")); err != nil {
					t.Fatal(err)
				}
				profile += "export PATH=\"$HOME/go/bin:$PATH\"\n"
				preflight = "[[ $(command -v openai) == \"$HOME/go/bin/openai\" && $(openai --version) == old ]] || exit 90\n"
			}
			profilePath := filepath.Join(home, ".zshrc")
			if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			run := func(script string) ([]byte, error) {
				cmd := exec.Command(zsh, "-d", "-i", "-c", script)
				cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "BREW_RELEASE_FIXTURE=" + available, "BREW_FAIL=" + tc.failure}
				return cmd.CombinedOutput()
			}
			const checkSelected = `[[ $(command -v openai) == "$HOME/.openai/bin/openai" && $(openai --version) == 'openai version 1.2.3' ]]`
			out, err := run(preflight + block + "\nrc=$?\n(( rc == 0 )) || exit $rc\n" + checkSelected)
			if calls, readErr := os.ReadFile(filepath.Join(home, "brew.log")); readErr != nil || string(calls) != tc.calls {
				t.Fatalf("brew calls %q: %v; install output: %s", calls, readErr, out)
			}
			if tc.failure != "" {
				exit, ok := err.(*exec.ExitError)
				if !ok || (tc.exitCode > 0 && exit.ExitCode() != tc.exitCode) {
					t.Fatalf("failed %s: %v: %s", tc.failure, err, out)
				}
				if actual, err := os.ReadFile(profilePath); err != nil || string(actual) != profile {
					t.Fatalf("changed profile after failure: %q: %v", actual, err)
				}
				if tc.failure == "old-tap" {
					missingHelper := filepath.Join(bin, "openai-enable-updates.zsh")
					if !strings.Contains(string(out), missingHelper) {
						t.Fatalf("missing setup helper path in shell error: %s", out)
					}
					if output, err := run(preflight + recovery); err != nil || !strings.Contains(string(output), "Usage from cask 1.0.0: openai [command]") {
						t.Fatalf("README recovery with earlier Go binary: %v: %s", err, output)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("paste: %v: %s", err, out)
			}
			if out, err := run(checkSelected); err != nil {
				t.Fatalf("fresh zsh: %v: %s", err, out)
			}
		})
	}
}

// TestArchiveProfileProcess runs as a subprocess of the executable fixture.
func TestArchiveProfileProcess(t *testing.T) {
	if len(os.Args) < 5 || os.Args[len(os.Args)-4] != "--" || os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") != "1" {
		t.Skip("archive fixture subprocess only")
	}
	updatenotice.RunUpdateHelper(append([]string{"openai"}, os.Args[len(os.Args)-3:]...))
	os.Exit(0)
}
