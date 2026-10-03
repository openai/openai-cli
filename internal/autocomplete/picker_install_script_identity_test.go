package autocomplete

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerScriptContentChecksLegacyNamespaces(t *testing.T) {
	options := PickerInstallation{Shell: CompletionStyleBash, Profile: "/original/profile"}
	data := []byte(pickerScriptHeader + "# synthetic legacy script\n")
	block := pickerInstalledBlock{Shell: options.Shell, Script: filepath.Join("/scripts", pickerScriptName(options, data))}
	changed := options
	changed.Profile = "/different/profile"
	require.False(t, pickerScriptMatchesProfile(changed, changed.Profile, block))
	require.True(t, validPickerScriptContent(changed, block, data))
	for label, invalid := range map[string]string{
		"wrong shell":       strings.Replace(block.Script, "picker-bash", "picker-zsh", 1),
		"nonhex namespace":  strings.Replace(block.Script, "picker-bash-", "picker-bash-z", 1),
		"missing namespace": strings.Replace(block.Script, pickerScriptProfilePrefix(options), "picker-bash-", 1),
		"wrong extension":   block.Script + ".bak",
	} {
		t.Run(label, func(t *testing.T) {
			bad := block
			bad.Script = invalid
			require.False(t, validPickerScriptContent(changed, bad, data))
		})
	}
	require.False(t, validPickerScriptContent(changed, block, append(bytes.Clone(data), '!')))
	require.False(t, validPickerScriptContent(changed, block, bytes.TrimPrefix(data, []byte(pickerScriptHeader))))
}
