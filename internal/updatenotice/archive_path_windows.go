//go:build windows

package updatenotice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows constructs a new user's PATH from the machine value followed by the
// user value. Updating HKCU cannot override a package earlier on the machine PATH.
func registerWindowsUserPath(bin string) (bool, error) {
	if strings.ContainsAny(bin, ";%") {
		return false, windowsSetupMessage(`the installation path contains a PATH separator or expansion character. Run & (Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe')`)
	}
	system, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer system.Close()
	machinePath, kind, err := system.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return false, err
	}
	if kind == registry.EXPAND_SZ {
		machinePath, err = registry.ExpandString(machinePath)
		if err != nil {
			return false, err
		}
	}
	conflict := windowsMachinePathConflict(machinePath, bin, os.Getenv("PATHEXT"))
	user, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer user.Close()
	changed, err := prependWindowsUserPath(user, bin)
	if err != nil {
		return false, err
	}
	if changed {
		// Existing terminal processes retain their environment. Explorer listens
		// for this notification when it prepares future interactive processes.
		broadcast := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
		environment, _ := windows.UTF16PtrFromString("Environment")
		const hwndBroadcast = 0xffff
		const wmSettingChange = 0x001a
		const smtoAbortIfHung = 2
		broadcast.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(environment)), smtoAbortIfHung, 250, 0)
	}
	return conflict, nil
}

func windowsMachinePathConflict(machinePath, bin, pathExt string) bool {
	extensions := []string{".exe", ".com", ".cmd", ".bat", ".ps1"}
	for _, ext := range strings.Split(pathExt, ";") {
		if strings.HasPrefix(ext, ".") && !strings.ContainsAny(ext, "\\/:") {
			extensions = append(extensions, ext)
		}
	}
	for _, entry := range filepath.SplitList(machinePath) {
		if entry == "" {
			continue
		}
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(bin)) {
			// openai.exe exists here when this check runs. Machine PATH entries
			// after this one cannot shadow it.
			return false
		}
		for _, extension := range extensions {
			if info, err := os.Lstat(filepath.Join(entry, "openai"+extension)); err == nil && !info.IsDir() {
				return true
			}
		}
	}
	return false
}

// Keep registry expansion expressions, value kind, and the original text of the
// remaining entries. In particular, never write the process's combined PATH to HKCU.
func prependWindowsUserPath(key registry.Key, bin string) (bool, error) {
	raw, kind, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		raw, kind = "", registry.EXPAND_SZ
	} else if err != nil {
		return false, err
	}
	// filepath.SplitList discards quotes. Keep the users' original text even
	// for empty entries and quoted paths which contain a semicolon.
	first := bin
	var remaining []string
	quoted, start := false, 0
	for i := 0; raw != "" && i <= len(raw); i++ {
		if i < len(raw) {
			if raw[i] == '"' {
				quoted = !quoted
			}
			if quoted || raw[i] != ';' {
				continue
			}
		}
		entry := raw[start:i]
		resolved := strings.ReplaceAll(entry, `"`, "")
		if kind == registry.EXPAND_SZ && resolved != "" {
			resolved, err = registry.ExpandString(resolved)
			if err != nil {
				return false, err
			}
		}
		if resolved != "" && strings.EqualFold(filepath.Clean(resolved), filepath.Clean(bin)) {
			if start == 0 {
				first = entry
			}
		} else {
			remaining = append(remaining, entry)
		}
		start = i + 1
	}
	updated := strings.Join(append([]string{first}, remaining...), ";")
	if updated == raw {
		return false, nil
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", updated)
	} else {
		err = key.SetStringValue("Path", updated)
	}
	return err == nil, err
}
