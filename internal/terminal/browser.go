// Package terminal contains shared terminal behavior and platform adapters.
package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OpenBrowser opens a trusted URL with the platform browser handler.
// Callers select the destination and bound the context; the launcher uses direct
// arguments, filters the child environment, and discards handler diagnostics.
func OpenBrowser(ctx context.Context, url string) error {
	name, args, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Env = browserEnvironment(runtime.GOOS, os.Environ())
	return command.Run()
}

func browserEnvironment(goos string, environment []string) []string {
	clean := make([]string, 0, len(environment))
	for _, setting := range environment {
		name, _, valid := strings.Cut(setting, "=")
		if valid && browserSettingAllowed(goos, name) {
			clean = append(clean, setting)
		}
	}
	return clean
}

// Preserve desktop discovery and profile locations without copying unrelated
// application settings. Linux retains xdg-open's documented browser fallback.
func browserSettingAllowed(goos, name string) bool {
	switch goos {
	case "darwin", "linux":
		switch name {
		case "PATH", "HOME", "TMPDIR", "USER", "LOGNAME", "TZ",
			"LANG", "LANGUAGE", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES",
			"LC_MONETARY", "LC_NUMERIC", "LC_TIME":
			return true
		}
		if goos == "darwin" {
			return name == "__CF_USER_TEXT_ENCODING"
		}
		switch name {
		case "DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR",
			"BROWSER", "DESKTOP_STARTUP_ID", "XDG_ACTIVATION_TOKEN",
			"DE", "WSL_INTEROP", "WSL_DISTRO_NAME", "XDG_MENU_PREFIX",
			"XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_CACHE_HOME", "XDG_STATE_HOME",
			"XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "XDG_SESSION_TYPE", "DESKTOP_SESSION",
			"XDG_UTILS_OVERRIDE_DE", "XDG_UTILS_OPEN_OVERRIDE_DE",
			"XDG_UTILS_MIME_OVERRIDE_DE", "XDG_UTILS_REALPATH_BACKEND",
			"KDE_FULL_SESSION", "KDE_SESSION_VERSION", "GNOME_DESKTOP_SESSION_ID", "MATE_DESKTOP_SESSION_ID",
			"DESKTOP", "LXQT_SESSION_CONFIG", "LC_ADDRESS", "LC_IDENTIFICATION", "LC_MEASUREMENT",
			"LC_NAME", "LC_PAPER", "LC_TELEPHONE":
			return true
		}
	case "windows":
		switch strings.ToUpper(name) {
		case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "ALLUSERSPROFILE",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
			"COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "TEMP", "TMP":
			return true
		}
	}
	return false
}

func browserCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	default:
		return "", nil, errors.New("browser opening is not supported on this platform")
	}
}
