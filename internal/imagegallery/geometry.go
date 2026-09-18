package imagegallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openai/openai-cli/internal/imagefont"
)

// DisplayFont is an immutable rendering of a prepared revision for one cell
// geometry. It never changes gallery metadata or the revision's character map.
// Commit the original revision after activating this font successfully.
type DisplayFont struct {
	FontPath, PostScript string
	Existing             bool
}

// FontForGeometry fits the same images into the existing character grids using
// the caller's measured cell dimensions, expressed at 32ppem. Font advances and
// line metrics stay fixed; only PNG glyph dimensions change. This avoids a
// feedback loop where adapting the font would change Terminal's cell geometry.
func (g *Gallery) FontForGeometry(ctx context.Context, revision *Revision, width, height int) (DisplayFont, error) {
	if err := g.check(ctx); err != nil {
		return DisplayFont{}, err
	}
	if revision == nil || revision.owner != g || g.pending != revision {
		return DisplayFont{}, errors.New("image geometry revision is stale or belongs to another gallery")
	}
	if width < 8 || width > 24 || height < 16 || height > 48 {
		return DisplayFont{}, errors.New("image geometry is outside the supported Terminal spacing range")
	}
	if width == 16 && height == 32 {
		return DisplayFont{revision.FontPath, revision.PostScript, revision.Existing}, nil
	}
	// The base revision has a fresh immutable identity whenever its contents
	// change. Geometry-specific names are deterministic so repeated previews
	// reuse their fonts, while other tabs can retain a different variant.
	digest := sha256.Sum256([]byte(fmt.Sprintf("image-tiles-v1:%s:%d:%d", revision.PostScript, width, height)))
	token := hex.EncodeToString(digest[:16])
	path := filepath.Join(g.directory, "fonts", "revision-"+token+".ttf")
	postScript := "OpenAIImages-" + revision.state.ID[:8] + "-" + token + "-Regular"
	if err := checkPrivate(path, false); err == nil {
		return DisplayFont{path, postScript, true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return DisplayFont{}, err
	}
	frames := make([]imagefont.Frame, 0, len(revision.state.Images))
	for _, saved := range revision.state.Images {
		if err := ctx.Err(); err != nil {
			return DisplayFont{}, err
		}
		decoded, err := g.cachedImage(saved)
		if err != nil {
			return DisplayFont{}, err
		}
		frames = append(frames, imagefont.Frame{Image: decoded, Columns: saved.Columns, Rows: saved.Rows, CodepointStart: saved.Start})
	}
	encoded, err := imagefont.Encode(ctx, frames, imagefont.Options{
		Family:     "OpenAI Image Geometry " + revision.state.ID[:8] + " " + token[:8],
		PostScript: postScript, TileWidth: width, TileHeight: height,
	})
	if err != nil {
		return DisplayFont{}, err
	}
	if err := writeNew(path, encoded.Data); err != nil {
		return DisplayFont{}, err
	}
	return DisplayFont{path, postScript, false}, nil
}
