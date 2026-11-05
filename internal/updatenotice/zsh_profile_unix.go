//go:build darwin || linux

package updatenotice

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/openai/openai-cli/internal/autocomplete"

	"golang.org/x/sys/unix"
)

// enableZshPath runs inside the profile directory selected by the installer.
// Both identities come from its ownership and ACL preflight before installation.
func enableZshPath(profileDir, identity string, login bool) bool {
	if login {
		// Reorder only the managed directory. Preserve all other PATH entries, including empty entries.
		const line = `if [[ -o interactive && ${(t)path} != *readonly* && ${(t)PATH} != *readonly* ]]; then path=("$HOME/.openai/bin" "${(@)path[@]:#"$HOME/.openai/bin"}"); fi`
		return !strings.HasSuffix(identity, "/new") && appendProfilePath(profileDir, identity, ".zlogin", line)
	}
	return appendProfilePath(profileDir, identity, ".zshrc", `export PATH="$HOME/.openai/bin:$PATH"`)
}

// Bash login files can source .bashrc. Select the managed copy only once,
// and leave noninteractive shells alone.
func enableInteractiveProfile(dir, identity, name string) bool {
	switch name {
	case ".zshrc", ".bashrc", ".bash_profile", ".bash_login", ".profile":
		const line = `case $- in *i*) case "$PATH" in "$HOME/.openai/bin"|"$HOME/.openai/bin":*) ;; *) export PATH="$HOME/.openai/bin:$PATH" ;; esac ;; esac`
		return appendProfilePath(dir, identity, name, line)
	}
	return false
}

func appendProfilePath(profileDir, identity, name, line string) bool {
	directory, expected, ok := strings.Cut(identity, "/")
	dir, err := os.OpenFile(".", profileDirectoryAccess|unix.O_DIRECTORY, 0)
	if err != nil {
		return false
	}
	defer dir.Close()
	parent, err := dir.Stat()
	if !automaticPlatform() || !filepath.IsAbs(profileDir) || !ok || err != nil || !parent.IsDir() || !ownedUpdatePath(parent) || profileID(parent) != directory {
		return false
	}
	directorySelected := func() bool {
		selected, err := os.Stat(profileDir)
		return err == nil && selected.IsDir() && ownedUpdatePath(selected) && os.SameFile(parent, selected) && profileACLAllowed(dir)
	}
	if !directorySelected() {
		return false
	}
	flags := os.O_RDWR | os.O_APPEND | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if expected == "new" {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(name, flags, 0600)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedUpdatePath(info) || (expected != "new" && profileID(info) != expected) {
		return false
	}
	locked, err := tryLock(file)
	if err != nil || !locked {
		return false
	}
	current := func() bool {
		named, err := os.Lstat(name)
		return directorySelected() && err == nil && named.Mode().IsRegular() && ownedUpdatePath(named) && os.SameFile(info, named) && profileACLAllowed(file)
	}
	// A later PATH assignment can shadow an earlier copy of this line.
	// Read only the tail, even when a generated profile is very large.
	info, err = file.Stat()
	if err != nil {
		return false
	}
	tail := make([]byte, min(info.Size(), int64(len(line)+2)))
	if _, err := file.ReadAt(tail, info.Size()-int64(len(tail))); err != nil {
		return false
	}
	tail = bytes.TrimSuffix(tail, []byte("\n"))
	if string(tail) == line || bytes.HasSuffix(tail, []byte("\n"+line)) ||
		autocomplete.PathPrecedesSuggestions(file, info.Size(), filepath.Join(profileDir, name), line, filepath.Join(os.Getenv("HOME"), ".openai", "bin", "openai")) {
		return current()
	}
	if !current() {
		return false
	}
	_, err = file.WriteString("\n" + line + "\n")
	return err == nil && file.Sync() == nil && current()
}

func profileID(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino))
}
