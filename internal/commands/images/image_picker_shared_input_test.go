package images

import (
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSharedFormInputImagesEditingPolicy(t *testing.T) {
	image := pickerForTest(t)
	image.draft = []rune("abc\ndef")
	image.settings.prompt = string(image.draft)
	image.cursor = 5
	image.editPrompt(tea.KeyPressMsg{Code: tea.KeyHome})
	require.Zero(t, image.cursor)
	image.cursor = 5
	image.editPrompt(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, "ef", string(image.draft))
}
func TestSharedFormInputImagesInsertion(t *testing.T) {
	image := pickerForTest(t)
	image.draft = []rune("a猫b")
	image.cursor = 2
	image.commandOffset = 9
	image.note = "old"
	image.editPrompt(tea.KeyPressMsg{Code: '界', Text: "界"})
	require.Equal(t, "a猫界b", image.settings.prompt)
	require.Equal(t, 3, image.cursor)
	require.Zero(t, image.commandOffset)
	require.Empty(t, image.note)
}
func TestSharedFormInputImageTabCycleDoesNotSubmit(t *testing.T) {
	image := pickerForTest(t)
	for _, want := range []string{"options", "command", "prompt"} {
		require.Nil(t, pickerKey(image, tea.KeyTab))
		require.Equal(t, want, image.focus)
		require.Empty(t, image.result.Args)
	}
	image.cycleFocus(true)
	require.Equal(t, "command", image.focus)
	image.focus = "unknown"
	image.cycleFocus(false)
	require.Equal(t, "unknown", image.focus)
}
