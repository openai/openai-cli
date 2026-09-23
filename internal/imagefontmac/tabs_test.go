package imagefontmac

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminalTabsRequiresCompleteValidInventory(t *testing.T) {
	for _, test := range []struct {
		name, reply string
		valid       bool
	}{
		{"empty", `{"ok":true,"tabs":[]}`, true},
		{"normal", `{"ok":true,"tabs":[{"tty":"/dev/ttys001","fontName":"GoMono"}]}`, true},
		{"failure", `{"ok":false,"tabs":[{"tty":"/dev/ttys001","fontName":"GoMono"}]}`, false},
		{"missing tabs", `{"ok":true}`, false},
		{"null tabs", `{"ok":true,"tabs":null}`, false},
		{"malformed", `private native diagnostic`, false},
		{"bad tty", `{"ok":true,"tabs":[{"tty":"private","fontName":"GoMono"}]}`, false},
		{"bad font", `{"ok":true,"tabs":[{"tty":"/dev/ttys001","fontName":"private\u001b"}]}`, false},
		{"partial valid", `{"ok":true,"tabs":[{"tty":"/dev/ttys001","fontName":"GoMono"},{}]}`, false},
		{"duplicates", `{"ok":true,"tabs":[{"tty":"/dev/ttys001","fontName":"GoMono"},{"tty":"/dev/ttys001","fontName":"GoMono"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tabs, err := terminalTabs(t.Context(), func() bool { return true }, func(_ context.Context, program string, args, env []string) ([]byte, error) {
				require.Equal(t, interpreter, program)
				require.Equal(t, []string{"-l", "JavaScript", "-e", tabsBridge}, args)
				return []byte(test.reply), nil
			})
			if test.valid {
				require.NoError(t, err)
				require.NotNil(t, tabs)
			} else {
				require.Error(t, err)
				require.Nil(t, tabs)
				require.False(t, strings.Contains(err.Error(), "private"))
			}
		})
	}
}

func TestTerminalTabsPropagatesCancellationAndSanitizesErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := terminalTabs(ctx, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = terminalTabs(t.Context(), func() bool { return false }, nil)
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = terminalTabs(t.Context(), func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("private native diagnostic")
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private")
}
