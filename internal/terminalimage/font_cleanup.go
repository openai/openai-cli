package terminalimage

import (
	"context"
	"path/filepath"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

func cleanupClosedFontGalleries(ctx context.Context, directory, tty string) error {
	current := imagegallery.TerminalSession{Directory: directory, TTY: tty}
	return imagegallery.CleanupClosed(ctx, filepath.Dir(directory), current, func(ctx context.Context) ([]string, []string, error) {
		tabs, err := imagefontmac.TerminalTabs(ctx)
		if err != nil {
			return nil, nil, err
		}
		ttys, fonts := make([]string, len(tabs)), make([]string, len(tabs))
		for i, tab := range tabs {
			ttys[i], fonts[i] = tab.TTY, tab.FontName
		}
		return ttys, fonts, nil
	}, imagefontmac.Unregister)
}
