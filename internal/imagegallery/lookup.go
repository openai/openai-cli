package imagegallery

import (
	"context"
	"encoding/json"
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

// FindFontGallery locates the exact selected immutable font, including legacy
// root galleries. It does not transfer TTY ownership or change native state.
func FindFontGallery(ctx context.Context, root, postScript string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parts := strings.Split(postScript, "-")
	if len(parts) != 4 || parts[0] != "OpenAIImages" || !isHex(parts[1], 8) || !isHex(parts[2], 32) {
		return "", nil
	}
	switch parts[3] {
	case "Regular", "Bold", "Italic", "BoldItalic":
	default:
		return "", nil
	}
	if err := checkPrivate(root, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	candidates := []string{root}
	for _, entry := range entries {
		if entry.IsDir() && isHex(entry.Name(), 32) {
			candidates = append(candidates, filepath.Join(root, entry.Name()))
		}
	}
	found := ""
	for _, directory := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := checkPrivate(directory, true); err != nil {
			return "", err
		}
		data, err := readPrivate(filepath.Join(directory, "state.json"), 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &identity) != nil || !isHex(identity.ID, 32) || !strings.HasPrefix(identity.ID, parts[1]) {
			continue
		}
		gallery, err := Open(ctx, directory)
		if err != nil {
			return "", err
		}
		path, lookupErr := gallery.LookupFontPS(ctx, postScript)
		closeErr := gallery.Close()
		if err := errors.Join(lookupErr, closeErr); err != nil {
			return "", err
		}
		if path != "" {
			if found != "" {
				return "", errors.New("the selected image font matches multiple galleries")
			}
			found = directory
		}
	}
	return found, nil
}
