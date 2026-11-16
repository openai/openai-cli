package terminal

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

const browserTestURL = "https://learn.chatgpt.com/docs/cli"

func TestBrowserCommandUsesDirectArguments(t *testing.T) {
	for _, tc := range []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{browserTestURL}},
		{"linux", "xdg-open", []string{browserTestURL}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", browserTestURL}},
	} {
		name, args, err := browserCommand(tc.goos, browserTestURL)
		if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s: got %q %v, %v", tc.goos, name, args, err)
		}
	}
	if _, _, err := browserCommand("unsupported", browserTestURL); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestMissingBrowserDoesNotFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := OpenBrowser(t.Context(), browserTestURL); err == nil {
		t.Fatal("browser launch succeeded without a launcher")
	}
}

func TestBrowserEnvironmentKeepsDesktopSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a POSIX launcher")
	}
	dir := t.TempDir()
	name, _, err := browserCommand(runtime.GOOS, browserTestURL)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "launcher-result")
	script := `#!/bin/sh
test -z "${OPENAI_API_KEY+x}" || exit 21
test -z "${OPENAI_ADMIN_KEY+x}" || exit 22
test -z "${OPENAI_WEBHOOK_SECRET+x}" || exit 23
test -z "${OPENAI_BASE_URL+x}" || exit 24
test -z "${openai_api_key+x}" || exit 25
test "$HOME" = "$PATH" || exit 27
test "$1" = 'https://learn.chatgpt.com/docs/cli' || exit 28
test -z "${GITHUB_TOKEN+x}" || exit 29
test -z "${AWS_SECRET_ACCESS_KEY+x}" || exit 30
test -z "${CUSTOM_SIGNING_SECRET+x}" || exit 31
test -z "${F29_BROWSER_UNRELATED+x}" || exit 32
printf 'passed\n' > "$HOME/launcher-result"
`
	if runtime.GOOS == "linux" {
		script += "test \"$DISPLAY\" = synthetic-display || exit 26\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("DISPLAY", "synthetic-display")
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_BASE_URL", "openai_api_key",
		"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "CUSTOM_SIGNING_SECRET", "F29_BROWSER_UNRELATED"} {
		t.Setenv(key, "synthetic-browser-isolation-value")
	}
	if err := OpenBrowser(t.Context(), browserTestURL); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report)
	if err != nil || string(data) != "passed\n" {
		t.Fatalf("launcher verification failed: %v", err)
	}
	if os.Getenv("OPENAI_API_KEY") != "synthetic-browser-isolation-value" {
		t.Fatal("launcher changed the parent's environment")
	}
}

func TestBrowserEnvironmentKeepsOnlyPlatformSettings(t *testing.T) {
	denied := []string{
		"OPENAI_API_KEY=fake", "OpenAi_ADMIN_KEY=fake", "OPENAI_EMPTY=", "OPENAI=fake",
		"GITHUB_TOKEN=fake", "AWS_SECRET_ACCESS_KEY=fake", "CUSTOM_SIGNING_SECRET=fake",
		"SSH_AUTH_SOCK=/synthetic/agent", "HTTPS_PROXY=https://fake:fake@invalid",
		"LD_PRELOAD=/synthetic/loader", "DYLD_INSERT_LIBRARIES=/synthetic/loader", "BASH_ENV=/synthetic/startup",
		"NODE_OPTIONS=synthetic", "QT_PLUGIN_PATH=/synthetic/plugins", "COMSPEC=synthetic-shell",
		"PATH_SECRET=fake", "LC_SECRET=fake", "XDG_SECRET=fake", "XDG_UTILS_OTHER_OVERRIDE_DE=fake", "UNRELATED=exact=value",
		"DE_SECRET=fake", "WSL_SECRET=fake", "WSLENV=GITHUB_TOKEN", "XDG_UTILS_SETTINGS_OVERRIDE_DE=generic",
		"=C:=C:\\synthetic", "HOME", "=empty-name", "WAYLAND_SOCKET=9",
	}
	for _, tc := range []struct {
		goos string
		want []string
		drop []string
	}{
		{"darwin", []string{"PATH=/synthetic bin", "HOME=/synthetic/home", "TMPDIR=", "USER=synthetic", "LOGNAME=synthetic",
			"LANG=en_US.UTF-8", "LANGUAGE=en", "LC_ALL=", "LC_CTYPE=UTF-8", "LC_MESSAGES=C", "LC_TIME=C", "TZ=UTC",
			"__CF_USER_TEXT_ENCODING=synthetic", "PATH=/second/bin"},
			[]string{"BROWSER=synthetic-browser", "XDG_UTILS_OVERRIDE_DE=generic", "XDG_UTILS_OPEN_OVERRIDE_DE=flatpak",
				"DE=generic", "WSL_INTEROP=/synthetic/interop", "WSL_DISTRO_NAME=synthetic", "XDG_MENU_PREFIX=synthetic-",
				"XDG_UTILS_MIME_OVERRIDE_DE=generic", "XDG_UTILS_REALPATH_BACKEND=busybox-realpath",
				"DISPLAY=synthetic", "XDG_CONFIG_HOME=/synthetic/config", "SystemRoot=C:\\Windows", "Path=/wrong-case", "home=/wrong-case"}},
		{"linux", []string{"PATH=/synthetic bin", "HOME=/synthetic/home", "TMPDIR=", "USER=synthetic", "LOGNAME=synthetic",
			"LANG=en_US.UTF-8", "LC_ALL=", "LC_CTYPE=UTF-8", "LC_MESSAGES=C", "LC_PAPER=C", "TZ=UTC",
			"DISPLAY=:synthetic", "WAYLAND_DISPLAY=wayland-synthetic", "XAUTHORITY=/synthetic/auth",
			"BROWSER=synthetic-browser --new-window %s", "DESKTOP_STARTUP_ID=synthetic-startup", "XDG_ACTIVATION_TOKEN=synthetic-focus",
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/synthetic/bus;unix:abstract=synthetic", "XDG_RUNTIME_DIR=/synthetic/run",
			"XDG_CONFIG_HOME=/synthetic/config", "XDG_CONFIG_DIRS=/one:/two", "XDG_DATA_HOME=/synthetic/data", "XDG_DATA_DIRS=/three:/four",
			"XDG_CACHE_HOME=/synthetic/cache", "XDG_STATE_HOME=/synthetic/state", "XDG_CURRENT_DESKTOP=GNOME:synthetic",
			"XDG_SESSION_DESKTOP=synthetic", "XDG_SESSION_TYPE=wayland", "DESKTOP_SESSION=synthetic",
			"XDG_UTILS_OVERRIDE_DE=generic", "XDG_UTILS_OPEN_OVERRIDE_DE=flatpak", "XDG_UTILS_OPEN_OVERRIDE_DE=",
			"DE=generic", "WSL_INTEROP=/synthetic/interop", "WSL_DISTRO_NAME=synthetic", "XDG_MENU_PREFIX=synthetic-",
			"XDG_UTILS_MIME_OVERRIDE_DE=generic", "XDG_UTILS_REALPATH_BACKEND=busybox-realpath",
			"KDE_FULL_SESSION=true", "KDE_SESSION_VERSION=6", "GNOME_DESKTOP_SESSION_ID=synthetic",
			"MATE_DESKTOP_SESSION_ID=synthetic", "DESKTOP=synthetic", "LXQT_SESSION_CONFIG=/synthetic/lxqt"},
			[]string{"__CF_USER_TEXT_ENCODING=synthetic", "SystemRoot=C:\\Windows", "Path=/wrong-case", "home=/wrong-case"}},
		{"windows", []string{"Path=C:\\synthetic bin", "PATHEXT=.EXE;.COM", "SystemRoot=C:\\Windows", "windir=C:\\Windows",
			"SystemDrive=C:", "USERPROFILE=C:\\synthetic user", "HOMEDRIVE=C:", "HOMEPATH=\\synthetic user",
			"APPDATA=C:\\synthetic\\roaming", "LOCALAPPDATA=C:\\synthetic\\local", "ProgramData=C:\\synthetic\\data",
			"ALLUSERSPROFILE=C:\\synthetic\\shared", "ProgramFiles=C:\\synthetic programs", "ProgramFiles(x86)=C:\\synthetic x86",
			"ProgramW6432=C:\\synthetic64", "CommonProgramFiles=C:\\synthetic common", "CommonProgramFiles(x86)=C:\\synthetic common x86",
			"CommonProgramW6432=C:\\synthetic common64", "TEMP=", "tmp=C:\\synthetic tmp"},
			[]string{"BROWSER=synthetic-browser", "XDG_UTILS_OVERRIDE_DE=generic", "XDG_UTILS_OPEN_OVERRIDE_DE=flatpak",
				"DE=generic", "WSL_INTEROP=/synthetic/interop", "WSL_DISTRO_NAME=synthetic", "XDG_MENU_PREFIX=synthetic-",
				"XDG_UTILS_MIME_OVERRIDE_DE=generic", "XDG_UTILS_REALPATH_BACKEND=busybox-realpath",
				"HOME=/synthetic/home", "DISPLAY=:synthetic", "XDG_CONFIG_HOME=/synthetic/config", "__CF_USER_TEXT_ENCODING=synthetic"}},
		{"unsupported", []string{}, []string{"PATH=/synthetic/bin", "HOME=/synthetic/home"}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			settings := append([]string{"GITHUB_TOKEN=fake"}, tc.want...)
			settings = append(settings, denied...)
			settings = append(settings, tc.drop...)
			original := append([]string(nil), settings...)
			if got := browserEnvironment(tc.goos, settings); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected child environment: %#v", got)
			}
			if !reflect.DeepEqual(settings, original) {
				t.Fatal("launcher changed its input environment")
			}
			for _, settings := range [][]string{nil, denied} {
				if got := browserEnvironment(tc.goos, settings); got == nil || len(got) != 0 {
					t.Fatalf("empty child environment must remain explicit: %#v", got)
				}
			}
		})
	}
}
