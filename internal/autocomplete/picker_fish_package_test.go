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
	if err != nil {
		if strings.Contains(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), "fish") {
			t.Fatal(err)
		}
		t.Skip("expect is required for native fish startup")
	}
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

func TestFishPackageExploreInsertsAtFirstPrompt(t *testing.T) {
	fixture := newSuggestionStartupFixture(t, CompletionStyleFish, false, false)
	packageScript, err := autoCompleteFS.ReadFile("shellscripts/fish_package_explore.fish")
	require.NoError(t, err)
	selection, err := autoCompleteFS.ReadFile("shellscripts/fish_explore_selection.fish")
	require.NoError(t, err)
	module := filepath.Join(fixture.home, "explore-selection.fish")
	require.NoError(t, os.WriteFile(module, selection, 0600))
	vendor := filepath.Join(fixture.home, "share", "fish", "vendor_conf.d")
	require.NoError(t, os.MkdirAll(vendor, 0700))
	code := strings.NewReplacer(
		"/usr/share/openai-cli/explore-selection.fish", quotePickerPath(CompletionStyleFish, module),
		"/usr/bin/openai", quotePickerPath(CompletionStyleFish, fixture.options.Executable),
	).Replace(string(packageScript))
	if runtime.GOOS == "darwin" {
		code = strings.ReplaceAll(code, "/usr/bin/test", "/bin/test")
	}
	require.NoError(t, os.WriteFile(filepath.Join(vendor, "openai-explore.fish"), []byte(code), 0600))
	// Fish skips a vendor file when a personal file has the same basename.
	// The personal image picker must never shadow the vendor Explore registration.
	personal := filepath.Join(filepath.Dir(fixture.options.Profile), "openai-picker.fish")
	require.NoError(t, os.WriteFile(personal, []byte("# personal image picker settings\n"), 0600))
	writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
	mock, err := os.ReadFile(fixture.options.Executable)
	require.NoError(t, err)
	script := strings.Replace(string(mock), "sed -n '1p'", `printf 'BARE_SELECTED\n' >&2
    printf 'openai models list\n'`, 1)
	script = strings.Replace(script, `if [ "$1" = examples ]; then`, `if [ "$1" = examples ]; then
    if [ -n "${OPENAI_CLI_COMMANDS_FD-}" ]; then touch "$INSTALL_TEST_HOME/captured-help"; fi`, 1)
	require.NoError(t, os.WriteFile(fixture.options.Executable, []byte(script), 0700))

	runSuggestionInstalledShell(t, fixture, `
send -- "openai examples\r"
expect -exact "DISPLAYED_COMMAND_END\r\n"
expect -exact "INSTALL_TEST> "
send -- "openai explore\r"
expect -exact "BARE_SELECTED\r\n"
expect -exact "INSTALL_TEST> "
send -- "\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\005\025"
`)
	buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<openai models list>|18\n", string(buffers))
	for _, unexpected := range []string{"executed", "generation-calls", "captured-help"} {
		_, err := os.Stat(filepath.Join(fixture.home, unexpected))
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	// A personal installation can take over during an existing session. It may
	// replace our exact wrapper, but must leave a user's replacement in place.
	loader, err := renderInstalledSuggestions(fixture.options.installation())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.home, "manual.fish"), loader, 0600))
	const reloadVendor = `source "$HOME/share/fish/vendor_conf.d/openai-explore.fish"`
	const loadPersonal = `source "$HOME/manual.fish"`
	const customDisable = "function openai_command_insertion_disable; set -g called_custom_disable 1; end"
	const fileCommand = "test (type --type openai) = file"
	config := filepath.Join(fixture.home, "config", "fish", "config.fish")
	original, err := os.ReadFile(config)
	require.NoError(t, err)
	for _, tc := range []struct{ name, startup, action, verify string }{
		{"session disable", "", "__openai_package_explore_owns_command; and openai_command_insertion_disable; and openai_command_insertion_disable; and " + reloadVendor, fileCommand},
		{"disabled before first prompt", "openai_command_insertion_disable", reloadVendor, fileCommand},
		{"disabled with personal loader pending", loadPersonal + "; openai_command_insertion_disable", fileCommand, "not set -q __openai_suggestions_enabled"},
		{"explicit takeover after pending disable", loadPersonal + "; openai_command_insertion_disable", fileCommand + "; and " + loadPersonal, "__openai_suggestions_owns_command"},
		{"custom disable in config", customDisable, reloadVendor, fileCommand + "; and openai_command_insertion_disable; and set -q called_custom_disable"},
		{"custom command survives disable", "", "function openai; printf custom; end; openai_command_insertion_disable", "test (openai) = custom"},
		{"personal takeover", "", loadPersonal, "__openai_suggestions_owns_command; and openai_command_insertion_disable; and " + fileCommand},
		{"custom command before takeover", "", "function openai; printf custom; end; " + loadPersonal, "test (openai) = custom"},
		{"custom disable before takeover", "", customDisable + "; " + loadPersonal, "__openai_package_explore_owns_command; and openai_command_insertion_disable; and set -q called_custom_disable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.startup != "" {
				require.NoError(t, os.WriteFile(config, []byte(string(original)+"\n"+tc.startup+"\n"), 0600))
				t.Cleanup(func() { require.NoError(t, os.WriteFile(config, original, 0600)) })
			}
			runSuggestionInstalledShell(t, fixture, `
send -- {`+tc.action+`; and printf 'action\n' >"$HOME/verified"}
send -- "\r"
expect -exact "INSTALL_TEST> "
send -- {`+tc.verify+`; and printf 'verified\n' >>"$HOME/verified"}
send -- "\r"
expect -exact "INSTALL_TEST> "
`)
			evidence := filepath.Join(fixture.home, "verified")
			got, err := os.ReadFile(evidence)
			require.NoError(t, err)
			require.Equal(t, "action\nverified\n", string(got))
			require.NoError(t, os.Remove(evidence))
		})
	}

	marker := filepath.Join(fixture.home, ".openai", "shell", "command-insertion.off-fish")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0700))
	require.NoError(t, os.Symlink(filepath.Join(fixture.home, "missing"), marker))
	runSuggestionInstalledShell(t, fixture, `
send -- "type --type openai > \"\$HOME/kind\"\r"
expect -exact "INSTALL_TEST> "
`)
	kind, err := os.ReadFile(filepath.Join(fixture.home, "kind"))
	require.NoError(t, err)
	require.Equal(t, "file\n", string(kind))
}
