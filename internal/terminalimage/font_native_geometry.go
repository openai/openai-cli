package terminalimage

import (
	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
)

// fontViewport reports cells and AppKit logical-point extents on Apple Terminal.
type fontViewport = Size

func preservedGeometry(size fontViewport, pointSize int, source imagefontmac.SourceFont) (imagefont.PreserveOptions, error) {
	return imageFontPreservedGeometry(size, pointSize, source)
}
