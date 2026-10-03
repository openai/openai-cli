package autocomplete

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// PickerInstallation contains only caller-selected paths. Shell discovery and
// consent belong to the command layer. Profile must be the startup file that
// this shell actually reads.
type PickerInstallation struct {
	Shell     CompletionStyle
	Directory string
	Profile   string
}

type PickerInstallResult struct {
	Changed    bool
	ScriptPath string
}

const (
	pickerInstallLimit = 1 << 20 // Local startup files, never API payloads.
	pickerBlockBegin   = "# >>> openai image picker v1 >>>"
	pickerBlockEnd     = "# <<< openai image picker v1 <<<"
	pickerScriptHeader = "# OpenAI CLI image picker integration v1\n"
)

var errPickerInstallChanged = errors.New("shell integration files changed during setup; existing files were kept")

type pickerInstalledBlock struct {
	Shell          CompletionStyle `json:"shell"`
	Script         string          `json:"script"`
	ProfileCreated bool            `json:"profile_created,omitempty"`
}

type pickerFileSnapshot struct {
	info os.FileInfo
	data []byte
}

// WithPickerSetupLock serializes a command's complete setup decision, including
// all startup profiles and its opt-out preference. Its lock is distinct from
// the per-file transaction locks used by InstallPicker and RemovePicker.
func WithPickerSetupLock(ctx context.Context, directory string, change func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsAny(directory, "\x00\r\n") {
		return errors.New("shell integration requires a clean absolute configuration directory")
	}
	root, err := openPickerScriptDirectory(ctx, directory, true)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := lockPickerInstallation(ctx, root, ".picker-setup.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	return change()
}

// InstallPicker persists normal completion and the optional picker hook. The
// immutable script is committed before the profile; failed profile updates
// remove only a newly created, unchanged script. Cooperating writers serialize
// from validation to replacement. External editors do not share these locks:
// identity and bytes are checked again immediately before atomic replacement.
func InstallPicker(ctx context.Context, options PickerInstallation) (PickerInstallResult, error) {
	return changePickerInstallation(ctx, options, false)
}

// RemovePicker removes only an intact managed block and its verified script.
// Modified blocks or scripts are preserved and reported for manual inspection.
func RemovePicker(ctx context.Context, options PickerInstallation) (PickerInstallResult, error) {
	return changePickerInstallation(ctx, options, true)
}

// IsPickerInstalled verifies an intact profile block and an owned script that
// matches this CLI's current content, without creating files or locking.
func IsPickerInstalled(ctx context.Context, options PickerInstallation) (bool, error) {
	if err := validatePickerInstallation(options); err != nil {
		return false, err
	}
	root, err := openPickerDirectory(ctx, filepath.Dir(options.Profile), false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	profile, err := readPickerFile(ctx, root, filepath.Base(options.Profile))
	if err != nil {
		return false, err
	}
	_, _, installed, err := parsePickerBlock(profile.data, options)
	if err != nil || installed == nil {
		return false, err
	}
	scripts, err := openPickerScriptDirectory(ctx, options.Directory, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer scripts.Close()
	script, err := readPickerFile(ctx, scripts, filepath.Base(installed.Script))
	if err != nil || script.info == nil {
		return false, err
	}
	if !validOwnedPickerScript(options, *installed, script.data) {
		return false, errors.New("installed shell integration script was modified")
	}
	current, err := renderInstalledPicker(options)
	if err != nil {
		return false, err
	}
	return bytes.Equal(script.data, current), ctx.Err()
}

func changePickerInstallation(ctx context.Context, options PickerInstallation, remove bool) (result PickerInstallResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validatePickerInstallation(options); err != nil {
		return result, err
	}
	profileRoot, err := openPickerDirectory(ctx, filepath.Dir(options.Profile), !remove)
	if remove && errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer profileRoot.Close()
	profileName := filepath.Base(options.Profile)
	profileLock, err := lockPickerInstallation(ctx, profileRoot, "."+profileName+".openai-picker.lock")
	if err != nil {
		return result, err
	}
	defer profileLock.Close()
	profile, err := readPickerFile(ctx, profileRoot, profileName)
	if err != nil {
		return result, err
	}
	before, after, installed, err := parsePickerBlock(profile.data, options)
	if err != nil {
		return result, err
	}
	if remove && installed == nil {
		return result, nil
	}
	scriptRoot, err := openPickerScriptDirectory(ctx, options.Directory, !remove)
	if remove && errors.Is(err, os.ErrNotExist) {
		desired := append(append([]byte(nil), before...), after...)
		if err := removePickerProfileBlock(ctx, profileRoot, profileName, profile, *installed, desired); err != nil {
			return result, err
		}
		result.Changed = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer scriptRoot.Close()
	scriptLock, err := lockPickerInstallation(ctx, scriptRoot, ".picker-install.lock")
	if err != nil {
		return result, err
	}
	defer scriptLock.Close()
	var previous pickerFileSnapshot
	if installed != nil {
		previous, err = readPickerFile(ctx, scriptRoot, filepath.Base(installed.Script))
		if err != nil {
			return result, err
		}
		if previous.info != nil && !validOwnedPickerScript(options, *installed, previous.data) {
			return result, errors.New("installed shell integration script was modified; existing files were kept")
		}
	}
	var desired []byte
	var next pickerInstalledBlock
	var newSnapshot pickerFileSnapshot
	created := false
	defer func() {
		if err != nil && created {
			err = errors.Join(err, removePickerSnapshot(context.Background(), scriptRoot, filepath.Base(next.Script), newSnapshot))
		}
	}()
	if remove {
		desired = append(append([]byte(nil), before...), after...)
	} else {
		script, err := renderInstalledPicker(options)
		if err != nil {
			return result, err
		}
		next = pickerInstalledBlock{
			Shell: options.Shell, Script: filepath.Join(options.Directory, pickerScriptName(options, script)),
			ProfileCreated: profile.info == nil || installed != nil && installed.ProfileCreated,
		}
		result.ScriptPath = next.Script
		newSnapshot, err = readPickerFile(ctx, scriptRoot, filepath.Base(next.Script))
		if err != nil {
			return result, err
		}
		if newSnapshot.info != nil && !bytes.Equal(newSnapshot.data, script) {
			return result, errors.New("shell integration script path is already occupied; existing files were kept")
		}
		block := renderPickerBlock(next)
		desired = append(append(append([]byte(nil), before...), block...), after...)
		if len(desired) > pickerInstallLimit {
			return result, errors.New("shell startup file exceeds the setup size limit")
		}
		if newSnapshot.info == nil {
			// Unique content addresses permit an exclusive create. No existing
			// script is replaced and a profile never names a partial file.
			newSnapshot, err = writePickerScript(ctx, scriptRoot, filepath.Base(next.Script), script)
			if err != nil {
				return result, err
			}
			created = true
		}
	}
	if !bytes.Equal(profile.data, desired) {
		if remove {
			err = removePickerProfileBlock(ctx, profileRoot, profileName, profile, *installed, desired)
		} else {
			err = replacePickerProfile(ctx, profileRoot, profileName, profile, desired)
		}
		if err != nil {
			return result, err
		}
		result.Changed = true
	} else if created {
		result.Changed = true
	}
	// After the profile commit the new script belongs to that profile. A
	// superseded cleanup failure must not remove its active replacement.
	created = false
	if installed != nil && installed.Script != next.Script && previous.info != nil {
		if err := removePickerSnapshot(ctx, scriptRoot, filepath.Base(installed.Script), previous); err != nil {
			return result, err
		}
		result.Changed = true
	}
	return result, nil
}

func validatePickerInstallation(options PickerInstallation) error {
	if _, ok := shellCompletions[options.Shell]; !ok {
		return errors.New("unsupported picker shell")
	}
	if options.Shell == CompletionStylePowershell {
		return errors.New("PowerShell uses normal Tab completion. Type openai images generate and press Enter to open the image picker.")
	}
	paths := []string{options.Directory, options.Profile}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("shell integration requires clean absolute paths without line breaks")
		}
	}
	if filepath.Dir(options.Profile) == options.Directory || options.Profile == options.Directory {
		return errors.New("shell integration scripts require a separate directory from the startup file")
	}

	return nil
}

func pickerScriptName(options PickerInstallation, data []byte) string {
	ext := map[CompletionStyle]string{CompletionStyleZsh: "zsh", CompletionStyleBash: "bash", CompletionStyleFish: "fish"}[options.Shell]
	profileHash := sha256.Sum256([]byte(options.Profile))
	return fmt.Sprintf("picker-%s-%x-%x.%s", options.Shell, profileHash[:8], sha256.Sum256(data), ext)
}

func validOwnedPickerScript(options PickerInstallation, block pickerInstalledBlock, data []byte) bool {
	return bytes.HasPrefix(data, []byte(pickerScriptHeader)) && filepath.Base(block.Script) == pickerScriptName(options, data)
}

func parsePickerBlock(data []byte, options PickerInstallation) ([]byte, []byte, *pickerInstalledBlock, error) {
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return nil, nil, nil, errors.New("shell startup file must contain UTF-8 text")
	}
	begin, end := bytes.Count(data, []byte("# >>> openai image picker")), bytes.Count(data, []byte("# <<< openai image picker"))
	if begin == 0 && end == 0 {
		return data, nil, nil, nil
	}
	if begin != 1 || end != 1 {
		return nil, nil, nil, errors.New("shell startup file has ambiguous integration markers; existing files were kept")
	}
	start := bytes.Index(data, []byte("\n"+pickerBlockBegin+"\n"))
	finish := bytes.Index(data, []byte(pickerBlockEnd+"\n"))
	if start < 0 || finish < start {
		return nil, nil, nil, errors.New("shell integration block was modified; existing files were kept")
	}
	finish += len(pickerBlockEnd) + 1
	block := data[start:finish]
	lines := bytes.Split(block, []byte("\n"))
	if len(lines) < 4 || !bytes.HasPrefix(lines[2], []byte("# openai-picker: ")) {
		return nil, nil, nil, errors.New("shell integration block was modified; existing files were kept")
	}
	metadata, err := base64.RawURLEncoding.DecodeString(string(bytes.TrimPrefix(lines[2], []byte("# openai-picker: "))))
	var installed pickerInstalledBlock
	if err != nil || json.Unmarshal(metadata, &installed) != nil || installed.Shell != options.Shell ||
		filepath.Dir(installed.Script) != options.Directory ||
		strings.ContainsAny(installed.Script, "\x00\r\n") || !bytes.Equal(block, renderPickerBlock(installed)) {
		return nil, nil, nil, errors.New("shell integration block was modified or belongs to another setup; existing files were kept")
	}
	return data[:start], data[finish:], &installed, nil
}

func renderPickerBlock(block pickerInstalledBlock) []byte {
	metadata, _ := json.Marshal(block)
	script := quotePickerPath(block.Shell, block.Script)
	var source string
	switch block.Shell {
	case CompletionStyleBash:
		// Bash may read .profile, which is also a POSIX login-shell file.
		// Keep the entire block parseable there and activate only in Bash.
		source = "case $- in\n  *i*) if [ -n \"${BASH_VERSION-}\" ] && [ -f " + script + " ] && [ -r " + script + " ] && command -v openai >/dev/null 2>&1; then\n    . " + script + "\n  fi ;;\nesac"
	case CompletionStyleFish:
		source = "if status is-interactive; and command -sq openai; and test -f " + script + "; and test -r " + script + "\n    source " + script + "\nend"
	default:
		source = "if [[ $- == *i* && -f " + script + " && -r " + script + " ]] && command -v openai >/dev/null 2>&1; then\n  source " + script + "\nfi"
	}
	return []byte("\n" + pickerBlockBegin + "\n# openai-picker: " + base64.RawURLEncoding.EncodeToString(metadata) + "\n" + source + "\n" + pickerBlockEnd + "\n")
}

func quotePickerPath(shell CompletionStyle, path string) string {
	switch shell {
	case CompletionStyleFish:
		return "'" + strings.ReplaceAll(strings.ReplaceAll(path, "\\", "\\\\"), "'", "\\'") + "'"
	default:
		return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	}
}

func renderInstalledPicker(options PickerInstallation) ([]byte, error) {
	completion, err := shellCompletions[options.Shell](nil, "openai")
	if err != nil {
		return nil, err
	}
	picker, err := renderPickerCompletion(options.Shell, "openai")
	if err != nil {
		return nil, err
	}
	preamble, marker := "", ""
	switch options.Shell {
	case CompletionStyleZsh:
		preamble = "if (( ! $+functions[compdef] )); then\n  autoload -Uz compinit\n  compinit -i\nfi\n"
		marker = "\nif [[ ${__openai_picker_enabled-} == 1 ]]; then export OPENAI_PICKER_INTEGRATION=zsh; fi\n"
	case CompletionStyleBash:
		marker = "\nif [[ ${__openai_picker_enabled-} == 1 ]]; then export OPENAI_PICKER_INTEGRATION=bash; fi\n"
	case CompletionStyleFish:
		// conf.d runs before config.fish and lazy fish_user_key_bindings.
		// Install once at the first prompt so the wrapper captures the final
		// user binding. Later prompts must not reclaim a replacement/disable.
		picker = `if not status is-interactive; or set -q __openai_picker_modes
    return
end
set --erase --global OPENAI_PICKER_INTEGRATION
function openai_picker_disable
    functions --erase __openai_picker_install_on_prompt
end
function __openai_picker_install_on_prompt --on-event fish_prompt
    functions --erase __openai_picker_install_on_prompt
` + picker + `
    if set -q __openai_picker_modes[1]; set -gx OPENAI_PICKER_INTEGRATION fish; end
end
`
	}
	return []byte(pickerScriptHeader + preamble + completion + "\n" + picker + marker), nil
}

func openPickerDirectory(ctx context.Context, path string, create bool) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !pickerInstallOwned(info) || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0) {
		return nil, errors.New("shell integration requires a directory writable only by its owner")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, errors.Join(errPickerInstallChanged, err)
	}
	directory, err := root.Open(".")
	if err == nil {
		err = errors.Join(checkPickerDirectoryPermissions(directory), directory.Close())
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

// Both managed directories must protect the sourced script: a writable parent
// could replace the entire shell directory. Configured locations above these
// directories are caller-selected trust boundaries, not an ancestor audit.
func openPickerScriptDirectory(ctx context.Context, path string, create bool) (*os.Root, error) {
	parent, err := openPickerManagedDirectory(ctx, filepath.Dir(path), create)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openPickerManagedDirectory(ctx, path, create)
}

func openPickerManagedDirectory(ctx context.Context, path string, create bool) (*os.Root, error) {
	root, err := openPickerDirectory(ctx, path, create)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		directory, err := root.Open(".")
		if err == nil {
			err = errors.Join(checkPickerFileMetadata(directory), directory.Close())
		}
		if err != nil {
			root.Close()
			return nil, err
		}
	}
	return root, nil
}

func readPickerFile(ctx context.Context, root *os.Root, name string) (pickerFileSnapshot, error) {
	var snapshot pickerFileSnapshot
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if !info.Mode().IsRegular() || !pickerInstallOwned(info) || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0) {
		return snapshot, errors.New("shell integration requires regular files writable only by their owner")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return snapshot, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return snapshot, errors.Join(errPickerInstallChanged, err)
	}
	if err := checkPickerFileMetadata(file); err != nil {
		return snapshot, err
	}
	if opened.Size() > pickerInstallLimit {
		return snapshot, errors.New("shell integration file exceeds the setup size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, pickerInstallLimit+1))
	if err != nil {
		return snapshot, err
	}
	current, statErr := root.Lstat(name)
	if statErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != int64(len(data)) || !current.ModTime().Equal(opened.ModTime()) {
		return snapshot, errors.Join(errPickerInstallChanged, statErr)
	}
	if len(data) > pickerInstallLimit {
		return snapshot, errors.New("shell integration file exceeds the setup size limit")
	}
	if err := checkPickerFileMetadata(file); err != nil {
		return snapshot, err
	}
	return pickerFileSnapshot{current, data}, ctx.Err()
}

func checkPickerSnapshot(ctx context.Context, root *os.Root, name string, expected pickerFileSnapshot) error {
	current, err := readPickerFile(ctx, root, name)
	if err != nil {
		return err
	}
	if (expected.info == nil) != (current.info == nil) || (expected.info != nil && (!os.SameFile(expected.info, current.info) || expected.info.Mode() != current.info.Mode())) || !bytes.Equal(expected.data, current.data) {
		return errPickerInstallChanged
	}
	return nil
}

func writePickerScript(ctx context.Context, root *os.Root, name string, data []byte) (pickerFileSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return pickerFileSnapshot{}, err
	}
	temporary := ".openai-picker-script-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return pickerFileSnapshot{}, err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr == nil {
		// New files can inherit ACLs or other metadata despite mode 0600.
		// Check the opened staging file before any profile can source it.
		writeErr = checkPickerFileMetadata(file)
	}
	original, fileStatErr := file.Stat()
	err = errors.Join(writeErr, fileStatErr, file.Close(), ctx.Err())
	if err != nil {
		return pickerFileSnapshot{}, err
	}
	// Publish only a complete script, exclusively. A crash during writing
	// leaves an unreferenced temporary file, never a broken final script.
	if err := root.Link(temporary, name); err != nil {
		return pickerFileSnapshot{}, err
	}
	return pickerFileSnapshot{original, data}, nil
}

func replacePickerProfile(ctx context.Context, root *os.Root, name string, previous pickerFileSnapshot, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary := ".openai-picker-" + rand.Text()
	mode := os.FileMode(0600)
	if previous.info != nil {
		mode = previous.info.Mode().Perm()
	}
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Chmod(mode) // Preserve mode even under a restrictive umask.
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close(), ctx.Err()); err != nil {
		return err
	}
	if err := checkPickerSnapshot(ctx, root, name, previous); err != nil {
		return err
	}
	if err := checkPickerReplacementMetadata(root, name, temporary, previous); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

func removePickerProfileBlock(ctx context.Context, root *os.Root, name string, previous pickerFileSnapshot, installed pickerInstalledBlock, data []byte) error {
	// Restore absence only when setup created the file and the owned block is
	// still its entire contents. Older metadata cannot prove creation ownership.
	if installed.ProfileCreated && len(data) == 0 {
		return removePickerSnapshot(ctx, root, name, previous)
	}
	return replacePickerProfile(ctx, root, name, previous, data)
}

func removePickerSnapshot(ctx context.Context, root *os.Root, name string, previous pickerFileSnapshot) error {
	if err := checkPickerSnapshot(ctx, root, name, previous); err != nil {
		return err
	}
	return root.Remove(name)
}

// Lock files intentionally remain: removing a lock can give waiting processes
// different inodes to lock. Kernel locks are released on close or process exit.
func lockPickerInstallation(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NONBLOCK, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := root.Lstat(name)
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() || !pickerInstallOwned(info) || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return nil, errors.New("shell integration lock must be a private regular file")
		}
		file, err = root.OpenFile(name, os.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			file.Close()
		}
	}()
	identity, err := file.Stat()
	if err != nil {
		return nil, err
	}
	check := func() error {
		current, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !identity.Mode().IsRegular() || !current.Mode().IsRegular() || !pickerInstallOwned(current) || !os.SameFile(identity, current) || (runtime.GOOS != "windows" && current.Mode().Perm()&0077 != 0) {
			return errPickerInstallChanged
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		locked, err := tryPickerInstallLock(file)
		if err != nil {
			return nil, err
		}
		if locked {
			if err := errors.Join(check(), ctx.Err()); err != nil {
				return nil, err
			}
			ok = true
			return file, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
