package autocomplete

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerFishTabAndLifecycle(t *testing.T) {
	directory := t.TempDir()
	runPickerShellPTY(t, "fish", directory, "", "", `
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\025printf 'AFTER_CANCEL\\n'\r"
expect -exact "AFTER_CANCEL\r\n"
expect -exact "PICKER_TEST> "
send -- "openai images gen\t"
expect -exact "generate"
send -- "\025openai_picker_disable; openai_picker_disable; bind --user \\t >\"\$PICKER_TEST_BINDING\" 2>/dev/null\r"
expect -exact "PICKER_TEST> "
send -- "exit\r"; expect eof
`)
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, "launch fish 2\n<images>\n<generate>\ntty:111\n", string(result))
	binding, err := os.ReadFile(filepath.Join(directory, "binding"))
	require.NoError(t, err)
	require.Empty(t, binding)
}

func TestPickerFishBindingsAndResourcing(t *testing.T) {
	for _, scenario := range []struct{ name, setup, probe, interaction, want string }{
		{
			name: "custom binding",
			setup: `fish_default_key_bindings
function custom_tab; printf 'fallback\n' >>"$PICKER_TEST_RESULT"; end
bind \t custom_tab`,
			probe:       `source "$PICKER_TEST_HOOK"`,
			interaction: `send -- "other\t\025openai_picker_disable; openai_picker_disable; bind --user \\t >>\"\$PICKER_TEST_RESULT\"\r"`,
			want:        "fallback\nbind tab custom_tab\n",
		},
		{
			name: "replacement binding",
			probe: `function new_tab; end
bind \t new_tab
source "$PICKER_TEST_HOOK"
openai_picker_disable
openai_picker_disable
bind --user \t >>"$PICKER_TEST_RESULT"`,
			want: "bind tab new_tab\n",
		},
		{
			name: "disable then enable",
			probe: `openai_picker_disable
source "$PICKER_TEST_HOOK"`,
			interaction: `send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\025printf 'DONE\\n'\r"`,
			want: "launch fish 2\n<images>\n<generate>\ntty:111\n",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			interaction := scenario.interaction
			if interaction != "" {
				interaction += "\nexpect -exact \"PICKER_TEST> \"\n"
			}
			interaction += `send -- "exit\r"; expect eof`
			runPickerShellPTY(t, "fish", directory, scenario.setup, scenario.probe, interaction)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, scenario.want, string(result))
		})
	}
}

func TestPickerFishRejectsOtherCommandLines(t *testing.T) {
	directory := t.TempDir()
	setup := `fish_default_key_bindings
function custom_tab; printf 'fallback\n' >>"$PICKER_TEST_RESULT"; end
bind \t custom_tab`
	var interaction strings.Builder
	for _, line := range []string{
		"openai images generate --help", "openai images generate >out", "openai images generate;",
		"openai images generate | cat", "true; openai images generate", "'openai' images generate",
		"OPENAI IMAGES GENERATE", "openai images generate $(touch unsafe)",
	} {
		interaction.WriteString("send -- {" + line + "}\nsend -- \"\\t\\025printf 'NEXT\\\\n'\\r\"\nexpect -exact \"NEXT\\r\\n\"\nexpect -exact \"PICKER_TEST> \"\n")
	}
	interaction.WriteString(`send -- "exit\r"; expect eof`)
	runPickerShellPTY(t, "fish", directory, setup, "", interaction.String())
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("fallback\n", 8), string(result))
	_, err = os.Stat(filepath.Join(directory, "unsafe"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
