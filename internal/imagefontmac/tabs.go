package imagefontmac

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

//go:embed tabs.js
var tabsBridge string

// TerminalTab identifies a live tab and its selected font for cache cleanup.
type TerminalTab struct {
	TTY      string `json:"tty"`
	FontName string `json:"fontName"`
}

// TerminalTabs reads every tab without selecting or changing any of them.
// Any partial or invalid inventory is an error, never evidence of closed tabs.
func TerminalTabs(ctx context.Context) ([]TerminalTab, error) {
	return terminalTabs(ctx, Supported, run)
}

func terminalTabs(ctx context.Context, supported func() bool, execute runner) ([]TerminalTab, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !supported() {
		return nil, ErrUnsupported
	}
	data, err := execute(ctx, interpreter, []string{"-l", "JavaScript", "-e", tabsBridge}, environment(os.Environ()))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("could not enumerate Terminal tabs for image cache cleanup")
	}
	var result struct {
		OK   bool          `json:"ok"`
		Tabs []TerminalTab `json:"tabs"`
	}
	if err := json.Unmarshal(data, &result); err != nil || !result.OK || result.Tabs == nil {
		return nil, errors.New("macOS Terminal tab bridge returned an incomplete result")
	}
	seen := map[string]bool{}
	for _, tab := range result.Tabs {
		if !terminalTTY.MatchString(tab.TTY) || seen[tab.TTY] || tab.FontName == "" || len(tab.FontName) > 255 || strings.ContainsAny(tab.FontName, "\x00\r\n\x1b") {
			return nil, errors.New("macOS Terminal tab bridge returned invalid tab settings")
		}
		seen[tab.TTY] = true
	}
	return result.Tabs, nil
}
