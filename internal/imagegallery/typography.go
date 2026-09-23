package imagegallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imagefont"
)

// TypographyFont keeps the gallery's immutable image assignments while using
// a local copy of the caller's text face. Plane-15 image characters leave the
// original font's BMP private-use icons available to shell prompts.
type TypographyFont struct {
	DisplayFont
	Text    string
	Related []DisplayFont
}

func (g *Gallery) FontForTypography(ctx context.Context, revision *Revision, source imagefont.PreserveOptions, companions ...imagefont.PreserveOptions) (TypographyFont, error) {
	if err := g.check(ctx); err != nil {
		return TypographyFont{}, err
	}
	if revision == nil || revision.owner != g || g.pending != revision {
		return TypographyFont{}, errors.New("image typography revision is stale or belongs to another gallery")
	}
	// JSON sorts map keys. Both source bytes and exact layout participate in
	// the identity, so font updates and different tabs cannot reuse stale tiles.
	// Version 3 draws each image row as a single bitmap. Old immutable tile
	// fonts must not mask that renderer update when the same image is previewed.
	identity, err := json.Marshal(struct {
		Version    int
		Revision   string
		Source     imagefont.PreserveOptions
		Companions []imagefont.PreserveOptions
	}{3, revision.PostScript, source, companions})
	if err != nil {
		return TypographyFont{}, errors.New("invalid image typography")
	}
	digest := sha256.Sum256(identity)
	token := hex.EncodeToString(digest[:16])
	text := strings.Map(func(r rune) rune {
		if r >= imagefont.FirstCodepoint && r <= imagefont.LastCodepoint {
			return 0xf0000 + r - imagefont.FirstCodepoint
		}
		return r
	}, revision.Text)
	frames := make([]imagefont.Frame, 0, len(revision.state.Images))
	for _, saved := range revision.state.Images {
		if err := ctx.Err(); err != nil {
			return TypographyFont{}, err
		}
		decoded, err := g.cachedImage(saved)
		if err != nil {
			return TypographyFont{}, err
		}
		frames = append(frames, imagefont.Frame{Image: decoded, Columns: saved.Columns, Rows: saved.Rows, CodepointStart: 0xf0000 + saved.Start - imagefont.FirstCodepoint})
	}
	fonts := make([]DisplayFont, 0, 1+len(companions))
	for _, face := range append([]imagefont.PreserveOptions{source}, companions...) {
		faceDigest := sha256.Sum256([]byte(token + ":" + face.SourcePostScript))
		faceToken := hex.EncodeToString(faceDigest[:16])
		path := filepath.Join(g.directory, "fonts", "revision-"+faceToken+".ttf")
		postScript := "OpenAIImages-" + revision.state.ID[:8] + "-" + faceToken + "-Regular"
		if err := checkPrivate(path, false); err == nil {
			fonts = append(fonts, DisplayFont{path, postScript, true})
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return TypographyFont{}, err
		}
		encoded, err := imagefont.EncodePreserving(ctx, frames, imagefont.Options{
			Family: "OpenAI Local " + revision.state.ID[:8] + " " + token[:8], PostScript: postScript,
		}, face)
		if err != nil {
			return TypographyFont{}, fmt.Errorf("preserve Terminal font %q: %w", face.SourcePostScript, err)
		}
		if err := writeNew(path, encoded.Data); err != nil {
			return TypographyFont{}, err
		}
		fonts = append(fonts, DisplayFont{path, postScript, false})
	}
	return TypographyFont{DisplayFont: fonts[0], Text: text, Related: fonts[1:]}, nil
}
