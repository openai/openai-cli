package imagefontmac

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestoreProfileKeepsOriginalFontInCapturedData(t *testing.T) {
	original := ProfileStatus{FontName: "Original'Font$(name)`", FontSize: 13, ProfileID: 102, ProfileName: "Basic"}
	_, err := inspectProfile(t.Context(), "restore", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular", func() bool { return true }, func(_ context.Context, program string, args, _ []string) ([]byte, error) {
		require.Equal(t, interpreter, program)
		require.Len(t, args, 9)
		require.Equal(t, []string{"restore", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular"}, args[4:8])
		var captured ProfileStatus
		require.NoError(t, json.Unmarshal([]byte(args[8]), &captured))
		require.Equal(t, original, captured)
		require.NotContains(t, profileBridge, original.FontName)
		return []byte(`{"ok":true}`), nil
	}, original)
	require.NoError(t, err)
}

func TestRestoreProfileRejectsInvalidOwnershipAndCapture(t *testing.T) {
	valid := ProfileStatus{FontName: "Menlo-Regular", FontSize: 13, ProfileID: 102, ProfileName: "Basic"}
	for _, test := range []struct {
		font     string
		original ProfileStatus
	}{
		{"Menlo-Regular", valid},
		{"OpenAIImages-ffffffff-next-Regular", valid},
		{"OpenAIImages-0123abcd-next-Regular", ProfileStatus{}},
		{"OpenAIImages-0123abcd-next-Regular", ProfileStatus{FontName: "bad\nfont", FontSize: 13, ProfileID: 102, ProfileName: "Basic"}},
		{"OpenAIImages-0123abcd-next-Regular", ProfileStatus{FontName: "Menlo-Regular", FontSize: 13, ProfileName: "Basic"}},
	} {
		_, err := inspectProfile(t.Context(), "restore", "OpenAI Images 0123abcd", "/dev/ttys002", test.font, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			t.Fatal("invalid rollback reached native bridge")
			return nil, nil
		}, test.original)
		require.Error(t, err)
	}
}
