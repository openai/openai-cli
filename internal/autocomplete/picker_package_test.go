package autocomplete

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPackagePickerFishFirstPrompt(t *testing.T) {
	binary, err := exec.LookPath("fish")
	if err != nil {
		if strings.Contains(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), "fish") {
			t.Fatal(err)
		}
		t.Skip("fish unavailable")
	}
	expect, err := exec.LookPath("expect")
	require.NoError(t, err)
	for _, scenario := range []string{"enabled", "late-opt-out", "late-dangling-opt-out", "late-other-binary", "late-custom-binding"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			cli := filepath.Join(root, "openai")
			require.NoError(t, os.WriteFile(cli, []byte("#!/bin/sh\nexit 99\n"), 0755))
			script, err := packagePickerScript(CompletionStyleFish, cli)
			require.NoError(t, err)
			startup := filepath.Join(root, "startup.fish")
			require.NoError(t, os.WriteFile(startup, script, 0644))
			setup := "source \"$PICKER_TEST_STARTUP\"\n" +
				"if set -q OPENAI_PICKER_INTEGRATION; echo EARLY-INTEGRATION; end\n" +
				"function fish_prompt; printf 'PICKER-READY> '; end\n"
			want := "fish"
			switch scenario {
			case "late-opt-out", "late-dangling-opt-out":
				config := filepath.Join(root, "custom-config", "openai")
				require.NoError(t, os.MkdirAll(config, 0700))
				marker := filepath.Join(config, "image-picker.json.tab-off-fish")
				if scenario == "late-opt-out" {
					require.NoError(t, os.WriteFile(marker, nil, 0600))
				} else {
					require.NoError(t, os.Symlink(filepath.Join(root, "absent"), marker))
				}
				setup += "set -gx XDG_CONFIG_HOME $HOME/custom-config\n"
				want = "off"
			case "late-other-binary":
				require.NoError(t, os.Mkdir(filepath.Join(root, "other"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(root, "other", "openai"), []byte("#!/bin/sh\nexit 98\n"), 0755))
				setup += "set -gx PATH $HOME/other $PATH\n"
				want = "off"
			case "late-custom-binding":
				setup += "fish_default_key_bindings\nfunction custom_binding; printf 'CUSTOM-BINDING\\n'; end\nbind \\t custom_binding\n"
			}
			init := filepath.Join(root, "init.fish")
			require.NoError(t, os.WriteFile(init, []byte(setup), 0644))
			probe := `if set -q OPENAI_PICKER_INTEGRATION; echo RESULT:$OPENAI_PICKER_INTEGRATION; else; echo RESULT:off; end; if functions -q __openai_package_picker_first_prompt; echo CALLBACK-REMAINS; end`
			driver := "set timeout 10\nspawn -noecho $env(PICKER_TEST_SHELL) --no-config --interactive --init-command {source \"$PICKER_TEST_INIT\"}\n" +
				"expect_before {\n  -exact \"\\033\\1330c\" {send -- \"\\033\\133?1;2c\"; exp_continue}\n  -exact \"\\033\\1336n\" {send -- \"\\033\\1331;1R\"; exp_continue}\n}\n" +
				"expect {\n  -exact \"PICKER-READY> \" {}\n  timeout {exit 3}\n}\n" +
				"send -- {" + probe + "}\nsend -- \"\\r\"\n" +
				"expect {\n  -exact \"RESULT:" + want + "\\r\\n\" {}\n  timeout {exit 4}\n}\n" +
				"expect {\n  -exact \"PICKER-READY> \" {}\n  timeout {exit 7}\n}\n"
			if scenario == "late-custom-binding" {
				driver += "send -- \"other-command\\t\"\nexpect {\n  -exact \"CUSTOM-BINDING\\r\\n\" {}\n  timeout {exit 5}\n}\nsend -- \"\\025\"\n"
			}
			driver += "send -- \"exit\\r\"\nexpect {\n  eof {}\n  timeout {exit 6}\n}\n"
			driverPath := filepath.Join(root, "driver.exp")
			require.NoError(t, os.WriteFile(driverPath, []byte(driver), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, expect, driverPath)
			command.Env = []string{"HOME=" + root, "PATH=" + root + ":" + os.Getenv("PATH"), "TERM=xterm-256color", "LC_ALL=C", "PICKER_TEST_SHELL=" + binary, "PICKER_TEST_STARTUP=" + startup, "PICKER_TEST_INIT=" + init}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.NotContains(t, string(output), "EARLY-INTEGRATION\r\n")
			require.NotContains(t, string(output), "CALLBACK-REMAINS\r\n")
		})
	}
}
