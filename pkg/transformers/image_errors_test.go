package transformers

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/stretchr/testify/require"
)

func TestFontPreviewDiagnosticRedactsPaths(t *testing.T) {
	const privatePath = "/Users/synthetic-private/image cache/preview.ttf"
	pathErr := &os.PathError{Op: "open", Path: privatePath, Err: os.ErrPermission}
	for name, err := range map[string]error{
		"path":          pathErr,
		"wrapped":       fmt.Errorf("read font %q: %w", privatePath, pathErr),
		"rename":        &os.LinkError{Op: "rename", Old: privatePath, New: privatePath + ".new", Err: os.ErrPermission},
		"unknown cause": &os.PathError{Op: "open", Path: privatePath, Err: errors.New("failure at " + privatePath)},
	} {
		t.Run(name, func(t *testing.T) {
			message := fontPreviewDiagnostic(&terminalimage.FontError{Err: err})
			require.Contains(t, message, "image cache")
			require.NotContains(t, message, "synthetic-private")
			require.NotContains(t, message, "preview.ttf")
		})
	}
	advice := "could not restore this tab's previous font; restore its font in Terminal's Inspector"
	message := fontPreviewDiagnostic(&terminalimage.FontError{Err: errors.Join(pathErr, errors.New(advice))})
	require.NotContains(t, message, privatePath)
	require.Contains(t, message, "check its permissions")
	require.Contains(t, message, advice)
}

func TestFontPreviewDiagnosticRetainsRecoveryAdvice(t *testing.T) {
	for _, advice := range []string{
		"allow this command to control Terminal in macOS Privacy & Security > Automation, then retry",
		"this tab's image font is full; open a new Terminal tab to continue displaying sharp images",
	} {
		require.Equal(t, advice, fontPreviewDiagnostic(&terminalimage.FontError{Err: errors.New(advice)}))
	}
}
