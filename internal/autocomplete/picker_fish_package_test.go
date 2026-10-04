package autocomplete

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFishPackagePickerFirstPrompt(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		if strings.Contains(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), "fish") {
			t.Fatal(err)
		}
		t.Skip("fish is unavailable")
	}
	expect, err := exec.LookPath("expect")
	require.NoError(t, err, "expect is required for native fish startup")
	for _, scenario := range []struct {
		name, setup string
		disabled    bool
	}{
		{"enabled", "", false},
		{"stable opt-out", `touch "$HOME/.openai/shell/image-picker.tab-off-fish"`, true},
		{"stable dangling opt-out", `ln -s "$HOME/missing" "$HOME/.openai/shell/image-picker.tab-off-fish"`, true},
		{"legacy opt-out", `touch "$HOME/.config/openai/image-picker.json.tab-off-fish"`, true},
		{"legacy dangling opt-out", `ln -s "$HOME/missing" "$HOME/.config/openai/image-picker.json.tab-off-fish"`, true},
		{"late XDG opt-out", "set -gx XDG_CONFIG_HOME $HOME/alternate\ntouch \"$XDG_CONFIG_HOME/openai/image-picker.json.tab-off-fish\"", true},
		{"stable opt-out after XDG change", "touch \"$HOME/.openai/shell/image-picker.tab-off-fish\"\nset -gx XDG_CONFIG_HOME $HOME/alternate", true},
		{"disabled in config", "openai_picker_disable", true},
		{"different executable", "set -gx PATH $HOME/other $PATH", true},
		{"different executable after prompt", "", false},
		{"function shadows executable", "function openai; end", true},
		{"PATH-selected test is ignored", "", false},
		{"relative home", "set -gx HOME relative", true},
		{"same executable through symlink", "rm \"$HOME/other/openai\"\nln -s \"$HOME/openai\" \"$HOME/other/openai\"\nset -gx PATH $HOME/other $PATH", false},
		{"repeated source", `source "$PICKER_TEST_PACKAGE"`, false},
		{"personal then package", "source \"$PICKER_TEST_PERSONAL\"\nsource \"$PICKER_TEST_PACKAGE\"", false},
		{"package then personal", `source "$PICKER_TEST_PERSONAL"`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			for _, directory := range []string{".openai/shell", ".config/openai", "alternate/openai", "other"} {
				require.NoError(t, os.MkdirAll(filepath.Join(home, directory), 0700))
			}
			fixture := "#!/bin/sh\nif [ \"$1\" = __complete ]; then printf 'generate\\n'; exit 0; fi\nprintf 'launch\\n' >>\"$PICKER_TEST_RESULT\"\nprintf '\\nPICKER_LAUNCHED\\n'\n"
			binary := filepath.Join(home, "openai")
			require.NoError(t, os.WriteFile(binary, []byte(fixture), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(home, "other", "openai"), []byte("#!/bin/sh\nexit 99\n"), 0700))
			if scenario.name == "PATH-selected test is ignored" {
				fixture := "#!/bin/sh\nprintf 'unexpected\\n' >>\"$HOME/identity-calls\"\nexit 99\n"
				require.NoError(t, os.WriteFile(filepath.Join(home, "test"), []byte(fixture), 0700))
			}
			packageScript, err := fishPackagePickerScript(binary)
			require.NoError(t, err)
			if runtime.GOOS == "darwin" {
				// The asset targets Linux; macOS keeps its system test in /bin.
				packageScript = []byte(strings.ReplaceAll(string(packageScript), "/usr/bin/test", "/bin/test"))
			}
			personalScript, err := renderInstalledPicker(PickerInstallation{Shell: CompletionStyleFish, OptOutPath: filepath.Join(home, ".openai", "shell", "image-picker.tab-off-fish")})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, "package.fish"), packageScript, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(home, "personal.fish"), personalScript, 0600))
			startup := `source "$PICKER_TEST_PACKAGE"
if set -q __openai_picker_modes; printf 'early\n' >>"$PICKER_TEST_RESULT"; end
fish_default_key_bindings
function custom_tab; printf 'fallback\n' >>"$PICKER_TEST_RESULT"; end
bind \t custom_tab
function fish_prompt
    if not set -q __package_test_prompt_seen
        set -g __package_test_prompt_seen 1
        if set -q __openai_picker_modes[1]; printf 'active\n'; else; printf 'off\n'; end >>"$PICKER_TEST_RESULT"
        if functions -q __openai_picker_install_on_prompt; printf 'pending\n' >>"$PICKER_TEST_RESULT"; end
    end
    printf 'PICKER_TEST> '
end
` + scenario.setup + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(home, "startup.fish"), []byte(startup), 0600))
			driver := `set timeout 8
spawn -noecho $env(PICKER_TEST_SHELL) --no-config --interactive --init-command {source "$PICKER_TEST_STARTUP"}
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { puts stderr "fish package probe timed out"; exit 1 }
expect -exact "PICKER_TEST> "
`
			// Exercise Tab at the very first prompt: a nested prompt callback
			// would leave this command on the ordinary custom-binding path.
			changedExecutable := scenario.name == "different executable after prompt"
			if changedExecutable {
				driver += `send -- {set -gx PATH "$HOME/other" $PATH}
send -- "\r"
expect -exact "PICKER_TEST> "
`
			}
			if !scenario.disabled && !changedExecutable {
				driver += `send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED\r\n"
expect -exact "PICKER_TEST> "
send -- "\025other\t\025printf 'FALLBACK_DONE\\n'\r"
expect -exact "FALLBACK_DONE\r\n"
expect -exact "PICKER_TEST> "
send -- "openai_picker_disable; printf 'DISABLED\\n'\r"
expect -exact "DISABLED\r\n"
expect -exact "PICKER_TEST> "
`
			}
			driver += `send -- "openai images generate\t\025printf 'RESTORED\\n'\r"
expect -exact "RESTORED\r\n"
expect -exact "PICKER_TEST> "
`
			if changedExecutable {
				driver += `send -- "openai_picker_disable\r"
expect -exact "PICKER_TEST> "
`
			}
			driver += `send -- {if set -q __openai_picker_modes[1]; printf 'reactivated\n' >>"$PICKER_TEST_RESULT"; end; if functions -q __openai_picker_install_on_prompt; printf 'pending\n' >>"$PICKER_TEST_RESULT"; end}
send -- "\r"
expect -exact "PICKER_TEST> "
send -- "exit\r"
expect eof
`
			require.NoError(t, os.WriteFile(filepath.Join(home, "driver.exp"), []byte(driver), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, expect, filepath.Join(home, "driver.exp"))
			command.Dir = home
			command.Env = []string{
				"HOME=" + home, "PATH=" + home + ":" + os.Getenv("PATH"), "TERM=xterm-256color", "LC_ALL=C",
				"PICKER_TEST_SHELL=" + fish, "PICKER_TEST_STARTUP=" + filepath.Join(home, "startup.fish"),
				"PICKER_TEST_PACKAGE=" + filepath.Join(home, "package.fish"), "PICKER_TEST_PERSONAL=" + filepath.Join(home, "personal.fish"),
				"PICKER_TEST_RESULT=" + filepath.Join(home, "result"),
			}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			result, err := os.ReadFile(filepath.Join(home, "result"))
			require.NoError(t, err)
			want := "active\nlaunch\nfallback\nfallback\n"
			if scenario.disabled {
				want = "off\nfallback\n"
			} else if changedExecutable {
				want = "active\nfallback\n"
			}
			require.Equal(t, want, string(result), string(output))
			if scenario.name == "PATH-selected test is ignored" {
				_, err := os.Stat(filepath.Join(home, "identity-calls"))
				require.ErrorIs(t, err, os.ErrNotExist, "startup and Tab must ignore a PATH-selected test executable")
			}
		})
	}
}

func TestFishPackagePreservesOrdinaryCompletions(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		if strings.Contains(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), "fish") {
			t.Fatal(err)
		}
		t.Skip("fish is unavailable")
	}
	home := t.TempDir()
	script, err := fishPackagePickerScript(filepath.Join(home, "package-openai"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "package.fish"), script, 0600))
	fixture := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$PICKER_TEST_CALLS\"\nprintf 'unrelated-completion\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, "openai"), []byte(fixture), 0700))
	command := exec.CommandContext(t.Context(), fish, "--no-config", "-c", `
complete -c openai -f -a preserved-completion
source "$HOME/package.fish"
complete -C 'openai '
`)
	command.Env = []string{"HOME=" + home, "PATH=" + home + ":" + os.Getenv("PATH"), "PICKER_TEST_CALLS=" + filepath.Join(home, "calls")}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "preserved-completion\n", string(output))
	_, err = os.Stat(filepath.Join(home, "calls"))
	require.ErrorIs(t, err, os.ErrNotExist, "package sourcing must not route ordinary completion to another executable")
}
