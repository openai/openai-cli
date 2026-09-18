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
	filenameStem           string
	options                []option.RequestOption
	preview                imagepreview.Protocol
	textPreview, textColor bool
	textTrueColor          bool
	openFiles              bool
	partialImages          int64
}

func imageGenerateOptions(ctx context.Context, cmd *cli.Command) ([]option.RequestOption, *imageOutputPlan, bool, error) {
	presentation := beginImageErrorContext(cmd)
	var body gjson.Result
	options, err := flagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON, false, func(raw []byte) { body = gjson.ParseBytes(raw) })
	if err != nil {
		return nil, nil, false, err
	}
	if err := validateImageSettings(body); err != nil {
		return nil, nil, false, err
	}
	plan, err := prepareImageOutput(cmd, isTerminal(cmd.Root().Writer), body)
	if err != nil {
		return nil, nil, false, err
	}
	streaming := body.Get("stream").Type == gjson.True || (plan != nil && plan.partialImages > 0)
	if plan == nil && body.Get("partial_images").Int() > 0 && !streaming {
		return nil, nil, false, fmt.Errorf("--partial-images needs --stream true for API output; in a terminal, omit data-format options to preview progress and save the final image automatically")
	}
	presentation.saving = plan != nil
	if plan != nil {
		if err := imageoutput.CheckName(ctx, plan.directory, plan.filenameStem); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, nil, false, err
			}
			label := "image filename"
			if cmd.IsSet("name") {
				label = "--name"
			}
			return nil, nil, false, fmt.Errorf("%s: %w", label, err)
		}
		if plan.textPreview {
			if err := prepareInteractiveImageFont(ctx, cmd.Root().Writer); err != nil {
				return nil, nil, false, fmt.Errorf("inline preview: %w; use --inline off to generate without a preview", err)
			}
		}
		if plan.openFiles {
			if err := imageopen.CheckAvailable(); err != nil {
				return nil, nil, false, fmt.Errorf("--open: %w", err)
			}
		}
		options = append(options, plan.options...)
		if streaming {
			options = append(options, option.WithJSONSet("stream", true))
		}
		// Give a waiting user immediate feedback. Keep scripts, explicit data
		// output and redirected diagnostics free of presentation-only text.
		if isTerminal(cmd.Root().Writer) && isTerminal(os.Stderr) && !imagePreviewCI(os.Getenv) && imageFriendlyErrorMode(cmd.Root()) {
			message := "Generating image..."
			if body.Get("n").Float() > 1 {
				message = "Generating images..."
			}
			if _, err := fmt.Fprintln(os.Stderr, message); err != nil {
				return nil, nil, false, err
			}
		}
	}
	return options, plan, streaming, nil
}

// The final body includes flags, piped JSON/YAML, and expanded file references.
// Body values supplied on stdin are not reflected in cmd.Value or cmd.IsSet.
func prepareImageOutput(cmd *cli.Command, terminal bool, body gjson.Result) (*imageOutputPlan, error) {
	partials := body.Get("partial_images").Int()
	if partials > 0 && body.Get("stream").Exists() && body.Get("stream").Type != gjson.True {
		return nil, fmt.Errorf("--partial-images needs streaming; omit --stream to enable it automatically when saving, or use --stream true")
	}
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
	name := cmd.String("name")
	var filenameStem string
	if cmd.IsSet("name") {
		var err error
		filenameStem, err = imageoutput.NormalizeName(name)
		if err != nil {
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
	// Preserve the existing raw --stream workflow. Progress previews or an
	// explicit saving flag opt into saving the streamed final image instead.
	if body.Get("stream").Type == gjson.True && partials == 0 && !explicitSave {
		return nil, nil
	}
	if (body.Get("stream").Type == gjson.True || partials > 0) && cmd.IsSet("max-items") {
		return nil, fmt.Errorf("--max-items limits API events and could stop before the final image; omit it when saving, or use --format json for API-event output")
	}
	if cmd.IsSet("output-dir") && cmd.String("output-dir") == "" {
		return nil, fmt.Errorf("--output-dir must name an existing directory")
	}
	if !cmd.IsSet("name") && body.Get("prompt").Type == gjson.String {
		// Use the final merged prompt, so flags and JSON/YAML/file input follow
		// the same naming policy. This is local text handling, not another API call.
		name = imageoutput.NameFromPrompt(body.Get("prompt").String())
		filenameStem = name
	}

	directory, err := imageoutput.ResolveDirectory(cmd.String("output-dir"))
	if err != nil {
		if cmd.IsSet("output-dir") {
			return nil, fmt.Errorf("%w\nChoose an existing, writable folder with --output-dir, or omit it to save automatically in ~/Downloads/gpt-images/.", err)
		}
		return nil, fmt.Errorf("%w\nChoose an existing, writable folder with --output-dir.", err)
	}
	plan := &imageOutputPlan{directory: directory, name: name, filenameStem: filenameStem, openFiles: cmd.Bool("open"), partialImages: partials}
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
			// Keep the everyday preset explicit without overriding supplied values,
			// including nulls from JSON/YAML. Other models retain their API defaults.
			for _, preset := range []struct {
				field string
				value any
			}{
				{"n", 1}, {"size", "auto"}, {"quality", "auto"}, {"output_format", "png"},
				{"background", "auto"}, {"moderation", "auto"}, {"partial_images", 0}, {"stream", false},
			} {
				if !body.Get(preset.field).Exists() {
					plan.options = append(plan.options, option.WithJSONSet(preset.field, preset.value))
				}
			}
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
	paths, saveErr := imageoutput.SaveResponse(ctx, response, p.directory, p.name)
	for _, path := range paths {
		// Quote paths so unusual filenames cannot inject terminal control codes.
		if _, err := fmt.Fprintf(out, "Saved image: %q\n", path); err != nil {
			return errors.Join(saveErr, err)
		}
		// Report every completed file before the batch error. Optional viewers
		// must not hide this failure or suggest regenerating saved images.
		if saveErr != nil {
			continue
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
	if saveErr != nil {
		if len(paths) > 0 {
			return fmt.Errorf("%w\nThe files listed above are saved. You do not need to generate those images again.", saveErr)
		}
		return fmt.Errorf("the API responded, but no images could be saved: %w", saveErr)
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
