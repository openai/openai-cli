package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/imageopen"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/imagepreview"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// This is CLI presentation policy; the API's default model and JSON contract
// remain defined by its schema. Keep the generated handler's integration small.
const defaultSavedImageModel = "gpt-image-2.5-sunburst"

type imageOutputPlan struct {
	directory              string
	name                   string
	options                []option.RequestOption
	preview                imagepreview.Protocol
	textPreview, textColor bool
	textTrueColor          bool
	openFiles              bool
}

func imageGenerateOptions(ctx context.Context, cmd *cli.Command) ([]option.RequestOption, *imageOutputPlan, error) {
	var body gjson.Result
	options, err := flagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON, false, func(raw []byte) { body = gjson.ParseBytes(raw) })
	if err != nil {
		return nil, nil, err
	}
	plan, err := prepareImageOutput(cmd, isTerminal(cmd.Root().Writer), body)
	if err != nil {
		return nil, nil, err
	}
	if plan != nil {
		if plan.textPreview {
			if err := prepareInteractiveImageFont(ctx, cmd.Root().Writer); err != nil {
				return nil, nil, fmt.Errorf("inline preview: %w; use --inline off to generate without a preview", err)
			}
		}
		if plan.openFiles {
			if err := imageopen.CheckAvailable(); err != nil {
				return nil, nil, fmt.Errorf("--open: %w", err)
			}
		}
		options = append(options, plan.options...)
	}
	return options, plan, nil
}

// The final body includes flags, piped JSON/YAML, and expanded file references.
// Body values supplied on stdin are not reflected in cmd.Value or cmd.IsSet.
func prepareImageOutput(cmd *cli.Command, terminal bool, body gjson.Result) (*imageOutputPlan, error) {
	inline := strings.ToLower(cmd.String("inline"))
	if inline == "" && !cmd.IsSet("inline") {
		inline = "on"
	}
	if inline != "on" && inline != "off" {
		return nil, fmt.Errorf("--inline must be on or off")
	}
	if cmd.IsSet("inline") && inline == "on" && cmd.Bool("no-preview") {
		return nil, fmt.Errorf("--inline on cannot be combined with --no-preview; use --inline off")
	}
	if cmd.IsSet("name") {
		if err := imageoutput.ValidateName(cmd.String("name")); err != nil {
			return nil, fmt.Errorf("--name: %w", err)
		}
	}
	explicitSave := cmd.IsSet("output-dir") || cmd.IsSet("name") || cmd.Bool("open")
	if !explicitSave && !terminal {
		return nil, nil
	}

	var conflict string
	if format := strings.ToLower(cmd.Root().String("format")); format != "" && format != "auto" {
		conflict = "--format " + format
	} else if cmd.Root().String("transform") != "" || cmd.Root().Bool("raw-output") {
		conflict = "--transform or --raw-output"
	} else if body.Get("stream").Bool() {
		conflict = "--stream"
	} else if body.Get("response_format").String() == "url" {
		conflict = "--response-format url"
	}
	if conflict != "" {
		if explicitSave {
			flag := "--output-dir"
			if !cmd.IsSet("output-dir") && cmd.IsSet("name") {
				flag = "--name"
			} else if !cmd.IsSet("output-dir") {
				flag = "--open"
			}
			return nil, fmt.Errorf("%s cannot be combined with %s; choose saved images or the API response", flag, conflict)
		}
		return nil, nil
	}
	if cmd.IsSet("output-dir") && cmd.String("output-dir") == "" {
		return nil, fmt.Errorf("--output-dir must name an existing directory")
	}

	directory, err := imageoutput.ResolveDirectory(cmd.String("output-dir"))
	if err != nil {
		return nil, err
	}
	plan := &imageOutputPlan{directory: directory, name: cmd.String("name"), openFiles: cmd.Bool("open")}
	if (!plan.openFiles || cmd.IsSet("inline")) && !cmd.Bool("no-preview") && terminal && !imagePreviewCI(os.Getenv) {
		preview := inline == "on"
		if !cmd.IsSet("inline") {
			preview, err = imageInlinePreference()
			if err != nil {
				return nil, fmt.Errorf("inline preference: %w; use --inline on or --inline off for this generation", err)
			}
		}
		if preview {
			plan.preview = imagePreviewProtocol(terminal, os.Getenv)
			plan.textPreview = plan.preview == ""
			plan.textColor = imagePreviewTextColor(os.Getenv)
			plan.textTrueColor = imagePreviewTrueColor(os.Getenv)
		}
	}
	model := body.Get("model")
	if !model.Exists() {
		// Explicit response-format is a legacy-model parameter. Preserve the API's
		// model selection when callers intentionally use it.
		if !body.Get("response_format").Exists() {
			plan.options = append(plan.options, option.WithJSONSet("model", defaultSavedImageModel))
		}
	} else if (model.String() == "dall-e-2" || model.String() == "dall-e-3") && !body.Get("response_format").Exists() {
		// Ask for embedded bytes rather than fetching a second, signed URL.
		plan.options = append(plan.options, option.WithJSONSet("response_format", "b64_json"))
	}
	return plan, nil
}

func (p *imageOutputPlan) save(ctx context.Context, response []byte, out io.Writer) error {
	return p.saveWithOpener(ctx, response, out, imageopen.Open)
}

func (p *imageOutputPlan) saveWithOpener(ctx context.Context, response []byte, out io.Writer, openImage func(context.Context, string) error) error {
	paths, err := imageoutput.SaveResponse(ctx, response, p.directory, p.name)
	if err != nil {
		return err
	}
	for _, path := range paths {
		// Quote paths so unusual filenames cannot inject terminal control codes.
		if _, err := fmt.Fprintf(out, "Saved image: %q\n", path); err != nil {
			return err
		}
		if p.openFiles {
			if err := openImage(ctx, path); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// The paid generation succeeded. Keep success and the saved path;
				// retrying a local viewer must never require another generation.
				if _, err := fmt.Fprintln(out, "Could not open an image viewer. The image is saved; retry with openai images preview --open FILE."); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(out, "Opening original image in your default viewer."); err != nil {
				return err
			}
		}
		if p.preview != "" || p.textPreview {
			if previewErr := renderImagePreview(ctx, out, path, p.preview, p.textColor, p.textTrueColor); previewErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Saving succeeded. An optional preview failure must not invite
				// another paid generation or expose decoder details.
				if err := reportImagePreviewFailure(out, previewErr); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func reportImagePreviewFailure(out io.Writer, previewErr error) error {
	message := "Preview unavailable; open the saved image to view it."
	var fontErr *imageFontPreviewError
	if errors.As(previewErr, &fontErr) {
		message = fontErr.Error() + "\nThe generated image is saved; no new generation is needed."
	}
	_, err := fmt.Fprintln(out, message)
	return err
}

// Read geometry after generation so resizing while waiting is respected.
// The local preview command uses this same path without another API request.
func renderImagePreview(ctx context.Context, out io.Writer, path string, protocol imagepreview.Protocol, textColor, trueColor bool) error {
	var size imagepreview.Size
	if file, ok := out.(*os.File); ok {
		size = imagepreview.TerminalSize(file.Fd())
	}
	if protocol == "" {
		if handled, err := tryImageFontPreview(ctx, out, path, size); handled {
			return err
		}
		if _, err := fmt.Fprintln(out, "Inline preview (text approximation):"); err != nil {
			return err
		}
		if err := imagepreview.RenderText(ctx, out, path, size, textColor, trueColor); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Use --open for full resolution in a separate window, or Ghostty/iTerm2 for a native inline image.")
		return err
	}
	return imagepreview.Render(ctx, out, path, protocol, size)
}

// Recognize terminal identities without queries or reads from stdin.
// Multiplexers need passthrough handling: inherited terminal identities do not
// prove that graphics will reach the outer terminal safely.
func imagePreviewProtocol(terminal bool, getenv func(string) string) imagepreview.Protocol {
	if !terminal {
		return ""
	}
	if imagePreviewCI(getenv) {
		return ""
	}
	t := getenv("TERM")
	if t == "dumb" || strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux") ||
		getenv("TMUX") != "" || getenv("STY") != "" || getenv("ZELLIJ") != "" {
		return ""
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return imagepreview.ITerm2
	case "ghostty":
		return imagepreview.Kitty
	case "": // Useful over SSH when TERM_PROGRAM was not forwarded.
	default:
		return "" // An explicitly different terminal takes precedence.
	}
	if t == "xterm-kitty" || t == "xterm-ghostty" {
		return imagepreview.Kitty
	}
	return ""
}

func imagePreviewCI(getenv func(string) string) bool {
	ci := strings.ToLower(getenv("CI"))
	return ci != "" && ci != "false" && ci != "0"
}

// Basic terminals still get ASCII. Use color only when advertised, without
// queries or input reads; imagePreviewTrueColor selects RGB where supported.
func imagePreviewTextColor(getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" || getenv("CLICOLOR") == "0" || getenv("TERM") == "dumb" {
		return false
	}
	color := strings.ToLower(getenv("COLORTERM"))
	return strings.Contains(getenv("TERM"), "256color") || color == "truecolor" || color == "24bit" || getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// Tahoe added RGB color to Apple Terminal (2.15, build 465). Inspect the
// terminal's advertised build, not the CLI host OS, so SSH remains correct.
// https://ratatui.rs/examples/layout/flex/
func imagePreviewTrueColor(getenv func(string) string) bool {
	if !imagePreviewTextColor(getenv) {
		return false
	}
	color := strings.ToLower(getenv("COLORTERM"))
	if color == "truecolor" || color == "24bit" {
		return true
	}
	term := getenv("TERM")
	if getenv("TERM_PROGRAM") != "Apple_Terminal" ||
		getenv("TMUX") != "" || getenv("STY") != "" || getenv("ZELLIJ") != "" ||
		strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") {
		return false
	}
	parts := strings.Split(getenv("TERM_PROGRAM_VERSION"), ".")
	for _, part := range parts {
		if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
			return false
		}
	}
	build, err := strconv.Atoi(parts[0])
	return err == nil && build >= 465
}
