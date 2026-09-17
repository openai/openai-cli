package cmd

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/imagepreview"
	"github.com/urfave/cli/v3"
)

// Always show the exact build the user invoked, unless PATH already resolves
// openai to that same executable. A checkout must not test an older install.
func imageInlineExecutable() string {
	executable, err := os.Executable()
	if err != nil || strings.IndexFunc(executable, unicode.IsControl) >= 0 {
		return "openai"
	}
	installed, _ := exec.LookPath("openai")
	cwd, _ := os.Getwd()
	temporary := os.Getenv("GOTMPDIR")
	if temporary == "" {
		temporary = os.TempDir()
	}
	return imageInlineExecutableCommand(executable, installed, cwd, temporary)
}

func imageInlineExecutableCommand(executable, installed, cwd, temporary string) string {
	// `go run` removes this executable when setup exits. Re-enter the same
	// positively identified checkout rather than suggesting its temporary path.
	if isTemporaryImageExecutable(executable, temporary) {
		if checkout := imageInlineCheckout(cwd); checkout != "" {
			return "go -C " + quoteImageShellArgument(checkout) + " run ./cmd/openai"
		}
	}
	if installed != "" {
		a, aerr := os.Stat(executable)
		b, berr := os.Stat(installed)
		if aerr == nil && berr == nil && os.SameFile(a, b) {
			return "openai"
		}
	}
	return quoteImageShellArgument(executable)
}

func isTemporaryImageExecutable(executable, temporary string) bool {
	// Resolve macOS's /var -> /private/var aliases before comparing paths.
	root, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		return false
	}
	path, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 4 || parts[2] != "exe" || parts[3] != "openai" && parts[3] != "openai.exe" {
		return false
	}
	for i, prefix := range []string{"go-build", "b"} {
		digits, ok := strings.CutPrefix(parts[i], prefix)
		if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
			return false
		}
	}
	return true
}

func imageInlineCheckout(cwd string) string {
	if cwd == "" || strings.IndexFunc(cwd, unicode.IsControl) >= 0 {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			// Stop at the nearest module, including nested modules that should
			// not be mistaken for the CLI checkout surrounding them.
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(strings.SplitN(line, "//", 2)[0])
				if len(fields) == 0 {
					continue
				}
				// Require the checkout's normal leading module directive; do
				// not mistake text inside a block comment for module identity.
				if len(fields) != 2 || fields[0] != "module" {
					return ""
				}
				if fields[1] != "github.com/openai/openai-cli" && fields[1] != `"github.com/openai/openai-cli"` {
					return ""
				}
				info, err := os.Stat(filepath.Join(dir, "cmd", "openai"))
				if err == nil && info.IsDir() {
					return dir
				}
				return ""
			}
			return ""
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(dir) == dir {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}

func quoteImageShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func testImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineCommand(cmd); err != nil {
		return err
	}
	if !isTerminal(cmd.Root().Writer) {
		return errors.New("run the visual check directly in an Apple Terminal tab with image previews enabled")
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	file := cmd.Root().Writer.(*os.File)
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return err
	}
	return runImageInlineTest(ctx, file, dir, tty, imagepreview.TerminalSize(file.Fd()), nativeImageFontServices())
}

func runImageInlineTest(ctx context.Context, out io.Writer, dir, tty string, size imagepreview.Size, services imageFontServices) error {
	gallery, err := imagegallery.Open(ctx, dir)
	if err != nil {
		return err
	}
	state := gallery.State()
	if err := gallery.Close(); err != nil {
		return err
	}
	if !state.Initialized {
		return errors.New("run openai images inline setup in this tab first")
	}
	if err := checkImageFontWidth(state, size); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".visual-check-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = png.Encode(file, imageInlineSample())
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// A strict check: no text fallback and no successful exit on failed display.
	if err := displayImageFont(ctx, out, dir, file.Name(), tty, size, services); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Text spacing: ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz 0123456789\nThe sample should have smooth color and no lines between tiles.\nYour selected profile and spacing are unchanged; previews fit the measured grid.\nIf it looks clear, this tab is ready for image commands.\nTo enable another tab: openai images inline setup\nThis check made no API call. Repeating it reuses the same cached sample.")
	return err
}

// A deterministic local check for tile edges, alpha and smooth gradients.
// It contains no user image or prompt and needs no downloaded assets.
func imageInlineSample() image.Image {
	const width, height = 384, 192
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.NRGBA{uint8(x * 255 / (width - 1)), uint8(y * 255 / (height - 1)), 180, 255}
			if x < 96 {
				if (x/12+y/12)%2 == 0 {
					c = color.NRGBA{255, 255, 255, 255}
				} else {
					c = color.NRGBA{30, 40, 55, 255}
				}
			}
			dx, dy := x-276, y-96
			if dx*dx+dy*dy < 64*64 {
				c = color.NRGBA{255, 135, 32, 255}
			}
			if x >= 96 && x < 144 {
				c.A = uint8(64 + y*191/(height-1))
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}
