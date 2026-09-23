package transformers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

func renderGeneratedImages(ctx context.Context, value gjson.Result, stdout *os.File) (bool, error) {
	protocol := imageProtocol()
	if protocol == "" {
		return false, nil
	}
	items := value.Get("data").Array()
	if len(items) == 0 {
		return false, nil
	}
	columns, rows, err := term.GetSize(stdout.Fd())
	if err != nil || columns < 2 || rows < 3 {
		columns, rows = 80, 24
	}
	// Prepare the complete response before emitting terminal graphics. A failed
	// download or decode must leave the successful API response available as JSON,
	// including when an earlier image in the same response was valid.
	images := make([]image.Image, 0, len(items))
	for _, item := range items {
		img, err := loadGeneratedImage(ctx, item)
		if err != nil {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}
			return false, nil
		}
		images = append(images, img)
	}
	for _, img := range images {
		// Approximate a cell as twice as tall as it is wide, and leave room for
		// the next shell prompt. This limits presentation size, not API payloads.
		width := max(1, min(columns-1, 80, (rows-2)*2*img.Bounds().Dx()/img.Bounds().Dy()))
		err = terminalimage.Write(ctx, stdout, img, protocol, width)
		var fontErr *terminalimage.FontError
		if errors.As(err, &fontErr) && ctx.Err() == nil {
			// An Automation denial must not discard a successfully generated
			// image. Redact filesystem paths before quoting terminal controls.
			fmt.Fprintf(os.Stderr, "Sharp image preview unavailable (%q); displaying a block preview.\n", fontPreviewDiagnostic(fontErr))
			err = terminalimage.Write(ctx, stdout, img, "blocks", width)
		}
		if err != nil {
			return true, err
		}
		if _, err := fmt.Fprintln(stdout); err != nil {
			return true, err
		}
	}
	return true, ctx.Err()
}

func imageProtocol() string {
	terminal := os.Getenv("TERM")
	if terminal == "dumb" {
		return ""
	}
	if os.Getenv("TMUX") != "" || strings.HasPrefix(terminal, "screen") || strings.HasPrefix(terminal, "tmux") {
		return "blocks"
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "WarpTerminal":
		return "iterm"
	case "kitty", "ghostty":
		return "kitty"
	case "Apple_Terminal":
		if terminalimage.FontSupported() {
			return "font"
		}
	}
	if terminal == "xterm-kitty" || terminal == "xterm-ghostty" {
		return "kitty"
	}
	return "blocks"
}

func loadGeneratedImage(ctx context.Context, item gjson.Result) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var reader io.Reader
	if encoded := item.Get("b64_json"); encoded.Type == gjson.String && encoded.Str != "" {
		reader = base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded.Str))
	} else {
		address := item.Get("url").String()
		parsed, err := url.Parse(address)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, errors.New("expected base64 image data or an HTTP(S) image URL")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, errors.New("invalid image URL")
		}
		// Image downloads use a separate, unauthenticated client. Never include a
		// signed URL in errors or forward API credentials to its destination.
		client := &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				request.Header.Del("Referer")
				if len(via) >= 10 {
					return errors.New("too many image redirects")
				}
				return nil
			},
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("image download failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("image download returned HTTP %d", response.StatusCode)
		}
		reader = response.Body
	}
	img, _, err := image.Decode(&imageReader{ctx, reader})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("invalid image data")
	}
	return img, nil
}

type imageReader struct {
	context.Context
	io.Reader
}

func (r *imageReader) Read(p []byte) (int, error) {
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
