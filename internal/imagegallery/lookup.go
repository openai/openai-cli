package imagegallery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LookupFontPS resolves a selected gallery font to its immutable private file.
// It lets callers restore login-session registration before asking CoreText
// for that font's lineage. Ordinary fonts and other galleries return no path.
// It does not inspect installed fonts, register anything, or change metadata.
func (g *Gallery) LookupFontPS(ctx context.Context, postScript string) (string, error) {
	if err := g.check(ctx); err != nil {
		return "", err
	}
	if !isHex(g.state.ID, 32) {
		return "", errors.New("initialize the image gallery before looking up its font")
	}
	prefix := "OpenAIImages-" + g.state.ID[:8] + "-"
	if !strings.HasPrefix(postScript, prefix) {
		return "", nil
	}
	parts := strings.Split(strings.TrimPrefix(postScript, prefix), "-")
	if len(parts) != 2 || !isHex(parts[0], 32) || (parts[1] != "Regular" && parts[1] != "Bold" && parts[1] != "Italic" && parts[1] != "BoldItalic") {
		return "", errors.New("the selected image font has an invalid gallery identity")
	}
	directory := filepath.Join(g.directory, "fonts")
	if err := checkPrivate(directory, true); err != nil {
		return "", fmt.Errorf("check image font directory: %w", safePathError(err))
	}
	path := filepath.Join(directory, "revision-"+parts[0]+".ttf")
	if err := checkPrivate(path, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("the previously selected image font is missing; select your original font and size in Terminal, then retry again: %w", os.ErrNotExist)
		}
		return "", fmt.Errorf("check cached image font: %w", safePathError(err))
	}
	return path, nil
}
