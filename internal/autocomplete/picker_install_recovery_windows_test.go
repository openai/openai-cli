//go:build windows

package autocomplete

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerProfileRecoveryRefusesEditorWithoutDeleteSharing(t *testing.T) {
	for _, action := range []string{"replace", "remove"} {
		t.Run(action, func(t *testing.T) {
			options, original, before := pickerRecoveryFixture(t, action)
			// Ordinary os.OpenFile intentionally omits FILE_SHARE_DELETE.
			// The transaction must refuse capture while this editor is open.
			editor, err := os.OpenFile(options.Profile, os.O_RDWR, 0)
			require.NoError(t, err)
			defer editor.Close()
			_, err = pickerRecoveryChange(t.Context(), options, action)
			require.Error(t, err)
			current, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, before, current)
			identity, err := pickerRecoveryFileIdentity(options.Profile)
			require.NoError(t, err)
			require.True(t, os.SameFile(original, identity), "refusal must leave the original inode in place")
			require.NoError(t, editor.Close())
			_, err = pickerRecoveryChange(t.Context(), options, action)
			require.NoError(t, err, "closing the incompatible editor must allow retry")
			retained := pickerRecoveryFindOriginal(t, options.Profile, original)
			require.NotEmpty(t, retained)
			preserved, err := os.ReadFile(retained)
			require.NoError(t, err)
			require.Equal(t, before, preserved)
		})
	}
}
