//go:build darwin

package custom

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
)

func TestImagePickerMacOSShellConfigChangesShareDecisionLock(t *testing.T) {
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config-a"))
			first, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			choice, err := imagePickerTabChoicePath(shell)
			require.NoError(t, err)
			acquired, release := make(chan struct{}), make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- autocomplete.WithPickerSetupLock(t.Context(), filepath.Dir(choice), func() error {
					close(acquired)
					<-release
					return nil
				})
			}()
			select {
			case <-acquired:
			case err := <-finished:
				require.NoError(t, err)
				t.Fatal("setup lock callback did not run")
			}
			defer func() {
				close(release)
				require.NoError(t, <-finished)
			}()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config-b"))
			second, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			secondChoice, err := imagePickerTabChoicePath(shell)
			require.NoError(t, err)
			require.Equal(t, choice, secondChoice)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			output, err := runPickerShellSetup(t, ctx, shell, "--uninstall-picker")
			require.ErrorIs(t, err, context.DeadlineExceeded, "shared preferences require the same decision lock across XDG roots")
			require.Empty(t, output)
			declined, err := imagePickerTabDeclined(shell)
			require.NoError(t, err)
			require.False(t, declined, "a blocked uninstall must not change the shared preference")
			require.Equal(t, filepath.Join(home, ".openai", "shell"), filepath.Dir(choice))
			require.Equal(t, first[0].Directory, second[0].Directory)
			if shell == "fish" {
				require.NotEqual(t, first[0].Profile, second[0].Profile, "fish startup selection must still follow XDG")
			}
			for _, target := range append(first, second...) {
				_, err := os.Stat(target.Profile)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestImagePickerMacOSShellSetupRetainsLegacyScriptRoot(t *testing.T) {
	home := pickerShellSetupHome(t)
	legacy, err := imagePickerSelectShellTargets("zsh", "", imagePickerShellPaths{
		home: home, config: filepath.Join(home, "old-xdg"), zdotdir: home,
	})
	require.NoError(t, err)
	personal := []byte("# personal startup\n")
	require.NoError(t, os.WriteFile(legacy[0].Profile, personal, 0600))
	installed, err := autocomplete.InstallPicker(t.Context(), legacy[0])
	require.NoError(t, err)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "new-xdg"))
	current, err := imagePickerShellTarget(t.Context(), "zsh", false, "")
	require.NoError(t, err)
	require.NotEqual(t, legacy[0].Directory, current[0].Directory)
	_, err = runPickerShellSetup(t, t.Context(), "zsh", "--install-picker")
	require.NoError(t, err)
	profile, err := os.ReadFile(legacy[0].Profile)
	require.NoError(t, err)
	require.Contains(t, string(profile), installed.ScriptPath)
	_, err = runPickerShellSetup(t, t.Context(), "zsh", "--uninstall-picker")
	require.NoError(t, err)
	profile, err = os.ReadFile(legacy[0].Profile)
	require.NoError(t, err)
	require.Equal(t, personal, profile)
	_, err = os.Stat(installed.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImagePickerMacOSFishOptOutAcrossConfigurationRoots(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is unavailable")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect is required for native fish startup")
	}
	for _, bindings := range []struct{ name, script string }{
		{"custom", "fish_default_key_bindings\nbind \\t custom_tab\n"},
		{"lazy", "set -g fish_key_bindings fish_vi_key_bindings\nfunction fish_user_key_bindings\n  bind -M insert \\t custom_tab\n  bind -M default \\t custom_tab\nend\n"},
	} {
		t.Run(bindings.name, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			firstConfig, secondConfig := filepath.Join(home, "fish-a"), filepath.Join(home, "fish-b")
			t.Setenv("XDG_CONFIG_HOME", firstConfig)
			_, err := runPickerShellSetup(t, t.Context(), "fish", "--install-picker")
			require.NoError(t, err)
			targets, err := imagePickerShellTarget(t.Context(), "fish", false, "")
			require.NoError(t, err)
			optOut, err := imagePickerTabChoicePath("fish")
			require.NoError(t, err)
			require.Equal(t, optOut, targets[0].OptOutPath)
			profileBefore, err := os.ReadFile(targets[0].Profile)
			require.NoError(t, err)
			scripts, err := filepath.Glob(filepath.Join(targets[0].Directory, "picker-fish-*.fish"))
			require.NoError(t, err)
			require.Len(t, scripts, 1)
			scriptBefore, err := os.ReadFile(scripts[0])
			require.NoError(t, err)
			require.Contains(t, string(scriptBefore), filepath.Base(optOut))
			startup := "function fish_prompt; printf 'PICKER_TEST> '; end\nfunction custom_tab; printf 'fallback\\n' >>$PICKER_TEST_RESULT; end\n" + bindings.script
			require.NoError(t, os.WriteFile(filepath.Join(firstConfig, "fish", "config.fish"), []byte(startup), 0600))
			fixture := "#!/bin/sh\nif [ \"$1\" = __complete ]; then printf 'synthetic-option\\n'; exit 0; fi\nprintf 'launch\\n' >>\"$PICKER_TEST_RESULT\"\nprintf '\\nPICKER_LAUNCHED\\n'\n"
			require.NoError(t, os.WriteFile(filepath.Join(home, "openai"), []byte(fixture), 0700))
			for _, phase := range []struct {
				name    string
				enabled bool
			}{
				{"installed", true}, {"removed from another root", false}, {"explicitly reenabled from another root", true},
			} {
				t.Run(phase.name, func(t *testing.T) {
					if phase.name != "installed" {
						t.Setenv("XDG_CONFIG_HOME", secondConfig)
						action := "--uninstall-picker"
						if phase.enabled {
							action = "--install-picker"
						}
						_, err := runPickerShellSetup(t, t.Context(), "fish", action)
						require.NoError(t, err)
					}
					declined, err := imagePickerTabDeclined("fish")
					require.NoError(t, err)
					require.Equal(t, !phase.enabled, declined)
					result := filepath.Join(t.TempDir(), "result")
					driver := `set timeout 8
spawn -noecho $env(PICKER_TEST_SHELL) --interactive
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { puts stderr "fish opt-out probe timed out"; exit 1 }
expect -exact "PICKER_TEST> "
send -- "complete -C 'openai synt'\r"
expect -exact "synthetic-option\r\n"
expect -exact "PICKER_TEST> "
send -- "other\t\025printf 'FALLBACK_DONE\\n'\r"
expect -exact "FALLBACK_DONE\r\n"
expect -exact "PICKER_TEST> "
send -- "openai images generate\t"
`
					if phase.enabled {
						driver += `expect -exact "PICKER_LAUNCHED\r\n"
expect -exact "PICKER_TEST> "
`
					}
					driver += `send -- "\025printf 'PROBE_DONE\\n'\r"
expect -exact "PROBE_DONE\r\n"
expect -exact "PICKER_TEST> "
send -- "exit\r"
expect eof
`
					driverPath := filepath.Join(t.TempDir(), "driver.exp")
					require.NoError(t, os.WriteFile(driverPath, []byte(driver), 0600))
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, expect, driverPath)
					command.Dir = home
					command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + firstConfig, "PATH=" + home + ":" + os.Getenv("PATH"), "TERM=xterm-256color", "LC_ALL=C", "PICKER_TEST_SHELL=" + fish, "PICKER_TEST_RESULT=" + result}
					output, err := command.CombinedOutput()
					require.NoError(t, err, string(output))
					data, err := os.ReadFile(result)
					require.NoError(t, err)
					want := "fallback\nfallback\n"
					if phase.enabled {
						want = "fallback\nlaunch\n"
					}
					require.Equal(t, want, string(data), "new sessions must respect the shared opt-out while preserving custom Tab")
					profileAfter, err := os.ReadFile(targets[0].Profile)
					require.NoError(t, err)
					require.Equal(t, profileBefore, profileAfter)
					scriptAfter, err := os.ReadFile(scripts[0])
					require.NoError(t, err)
					require.Equal(t, scriptBefore, scriptAfter, "the old-root script must honor preferences without being rewritten")
				})
			}
		})
	}
}
