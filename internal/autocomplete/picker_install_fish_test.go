package autocomplete

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPickerInstalledFishValidatesOptOutPath(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleFish)
	for _, path := range []string{"", filepath.Join(filepath.Dir(options.Profile), "opt out ' $ (inert)")} {
		options.OptOutPath = path
		require.NoError(t, validatePickerInstallation(options))
	}
	for i, path := range []string{"relative", options.Profile + "\n", options.Profile + "\x00", options.Profile + string(os.PathSeparator) + ".", options.Profile + string([]byte{0xff})} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			options.OptOutPath = path
			_, err := InstallPicker(t.Context(), options)
			require.Error(t, err)
			_, err = os.Stat(options.Directory)
			require.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(options.Profile)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPickerInstalledFishRefreshAddsSharedOptOut(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleFish)
	legacy, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	options.OptOutPath = filepath.Join(filepath.Dir(options.Profile), "tab-off-fish")
	current, err := IsPickerInstalled(t.Context(), options)
	require.NoError(t, err)
	require.False(t, current, "unguarded legacy scripts require a refresh")
	updated, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	require.True(t, updated.Changed)
	require.NotEqual(t, legacy.ScriptPath, updated.ScriptPath)
	_, err = os.Stat(legacy.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	current, err = IsPickerInstalled(t.Context(), options)
	require.NoError(t, err)
	require.True(t, current)
	repeated, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	require.False(t, repeated.Changed)
	_, err = RemovePicker(t.Context(), options)
	require.NoError(t, err)
	_, err = os.Stat(options.Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(updated.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerInstalledFishHonorsSharedOptOut(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is unavailable")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect is required for native fish startup")
	}
	for _, marker := range []string{"absent", "before startup", "before prompt", "disabled in config", "nonempty", "dangling link"} {
		t.Run(marker, func(t *testing.T) {
			home := t.TempDir()
			configuration := filepath.Join(home, "config")
			options := PickerInstallation{
				Shell: CompletionStyleFish, Directory: filepath.Join(configuration, "openai", "shell"),
				Profile:    filepath.Join(configuration, "fish", "conf.d", "openai-picker.fish"),
				OptOutPath: filepath.Join(home, "opt out ' $ (inert) \\"),
			}
			_, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			switch marker {
			case "before startup", "disabled in config":
				require.NoError(t, os.WriteFile(options.OptOutPath, nil, 0600))
			case "nonempty":
				require.NoError(t, os.WriteFile(options.OptOutPath, []byte("invalid"), 0600))
			case "dangling link":
				if err := os.Symlink(filepath.Join(home, "missing"), options.OptOutPath); err != nil {
					t.Skipf("symbolic links unavailable: %v", err)
				}
			}
			startup := `function custom_tab; end
fish_default_key_bindings
bind \t custom_tab
function fish_prompt
    if set -q __openai_picker_modes[1]; printf 'hook\n' >>"$PICKER_TEST_RESULT"; end
    if set -q OPENAI_PICKER_INTEGRATION; printf 'marker\n' >>"$PICKER_TEST_RESULT"; end
    if functions -q __openai_picker_install_on_prompt; printf 'pending\n' >>"$PICKER_TEST_RESULT"; end
    if bind \t | string match --quiet '*custom_tab*'; printf 'binding\n' >>"$PICKER_TEST_RESULT"; end
    if complete -C 'openai ' | string match --quiet '*images*'; printf 'completion\n' >>"$PICKER_TEST_RESULT"; end
    printf 'PICKER_TEST> '
end
`
			if marker == "before prompt" {
				startup += "printf '' >\"$PICKER_TEST_OFF\"\n"
			}
			if marker == "disabled in config" {
				startup += "openai_picker_disable; or printf 'disable-error\\n' >>\"$PICKER_TEST_RESULT\"\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(configuration, "fish", "config.fish"), []byte(startup), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(home, "openai"), []byte("#!/bin/sh\nif [ \"$1\" = __complete ]; then printf 'images\\n'; fi\n"), 0700))
			driver := `set timeout 8
spawn -noecho $env(PICKER_TEST_SHELL) --interactive
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { puts stderr "fish opt-out probe timed out"; exit 1 }
expect -exact "PICKER_TEST> "
send -- "exit\r"
expect eof
`
			require.NoError(t, os.WriteFile(filepath.Join(home, "driver.exp"), []byte(driver), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, expect, filepath.Join(home, "driver.exp"))
			command.Dir = home
			command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + configuration, "PATH=" + home + ":" + os.Getenv("PATH"), "TERM=xterm-256color", "LC_ALL=C", "PICKER_TEST_SHELL=" + fish, "PICKER_TEST_RESULT=" + filepath.Join(home, "result"), "PICKER_TEST_OFF=" + options.OptOutPath, "OPENAI_PICKER_INTEGRATION=fish"}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			result, err := os.ReadFile(filepath.Join(home, "result"))
			require.NoError(t, err)
			want := []string{"binding", "completion"}
			if marker == "absent" {
				want = []string{"hook", "marker", "completion"}
			}
			require.Equal(t, want, strings.Fields(string(result)))
		})
	}
}

func TestPickerInstalledFishRunsAfterUserConfiguration(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is unavailable")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect is required for native fish startup")
	}
	for _, config := range []struct {
		name, bindings string
		disabled       bool
	}{
		{"config binding", "fish_default_key_bindings\nbind \\t custom_tab\n", false},
		{"lazy user bindings", "set -g fish_key_bindings fish_vi_key_bindings\nfunction fish_user_key_bindings\n  bind -M insert \\t custom_tab\n  bind -M default \\t custom_tab\nend\n", false},
		{"disabled in config", "fish_default_key_bindings\nbind \\t custom_tab\nopenai_picker_disable\n", true},
	} {
		t.Run(config.name, func(t *testing.T) {
			home := t.TempDir()
			configuration := filepath.Join(home, "config")
			options := PickerInstallation{Shell: CompletionStyleFish, Directory: filepath.Join(configuration, "openai", "shell"), Profile: filepath.Join(configuration, "fish", "conf.d", "openai-picker.fish")}
			_, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			startup := "function fish_prompt; printf 'PICKER_TEST> '; end\nfunction custom_tab; printf 'fallback\\n' >>$PICKER_TEST_RESULT; end\n" + config.bindings
			require.NoError(t, os.WriteFile(filepath.Join(configuration, "fish", "config.fish"), []byte(startup), 0600))
			fixture := "#!/bin/sh\nprintf 'launch\\n' >>\"$PICKER_TEST_RESULT\"\nprintf '\\nPICKER_LAUNCHED\\n'\n"
			require.NoError(t, os.WriteFile(filepath.Join(home, "openai"), []byte(fixture), 0700))
			driver := `set timeout 8
spawn -noecho $env(PICKER_TEST_SHELL) --interactive
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { puts stderr "fish startup probe timed out"; exit 1 }
expect -exact "PICKER_TEST> "
send -- "other\t\025printf 'FALLBACK_DONE\\n'\r"
expect -exact "FALLBACK_DONE\r\n"
expect -exact "PICKER_TEST> "
`
			if !config.disabled {
				driver += `
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED\r\n"
expect -exact "PICKER_TEST> "
`
			}
			driver += `
send -- "\025openai_picker_disable; printf 'DISABLED\\n'\r"
expect -exact "DISABLED\r\n"
expect -exact "PICKER_TEST> "
send -- "openai images generate\t\025printf 'RESTORED\\n'\r"
expect -exact "RESTORED\r\n"
expect -exact "PICKER_TEST> "
send -- "exit\r"
expect eof
`
			require.NoError(t, os.WriteFile(filepath.Join(home, "driver.exp"), []byte(driver), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, expect, filepath.Join(home, "driver.exp"))
			command.Dir = home
			command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + configuration, "PATH=" + home + ":" + os.Getenv("PATH"), "TERM=xterm-256color", "LC_ALL=C", "PICKER_TEST_SHELL=" + fish, "PICKER_TEST_RESULT=" + filepath.Join(home, "result")}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			result, err := os.ReadFile(filepath.Join(home, "result"))
			require.NoError(t, err)
			want := "fallback\nlaunch\nfallback\n"
			if config.disabled {
				want = "fallback\nfallback\n"
			}
			require.Equal(t, want, string(result))
		})
	}
}
