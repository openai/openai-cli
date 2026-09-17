package cmd

import (
	"sort"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Presentation belongs beside the handwritten saving policy. Keep the generated
// API flags intact so new parameters retain their generated help automatically.
func init() {
	imagesGenerate.Usage = "Generate images from a text prompt."
	imagesGenerate.UsageText = "openai images generate --prompt TEXT [options]"
	imagesGenerate.Description = `In a terminal, save images to ~/Downloads/gpt-images/ and print their paths.
The default folder is created if needed. Custom folders must already exist.
Inline previews are on initially. Remember your choice: openai images inline on (or off).
Use --inline on or --inline off to override your choice for one generation.
iTerm2, Ghostty, and Kitty show images. Apple Terminal: images inline setup (experimental).
Other terminals show a text approximation.
Use --open for the full-resolution image in your default desktop viewer instead.
Use --name orange-robot for orange-robot.png; otherwise filenames use the date and time.
Saving uses ` + defaultSavedImageModel + ` when both model and legacy response format are omitted.

Use --format json for the API response without saving.
Use --output-dir to save when stdout is piped or redirected.
Saving flags (--output-dir, --name, --open) cannot be combined with JSON output or streaming.
Preview an existing file without an API call: openai images preview FILE
Open the original in your desktop viewer: openai images preview --open FILE

Examples:
  openai images generate --prompt "A tiny orange robot"
  openai images generate --prompt "A tiny orange robot" --open
  openai images generate --prompt "A tiny orange robot" --inline off --name orange-robot
  openai images generate --prompt "A tiny orange robot" \
    --output-dir "$HOME/Pictures"
  openai --format json images generate \
    --model ` + defaultSavedImageModel + ` --prompt "A tiny orange robot"

Model options and limits:
  https://developers.openai.com/api/docs/guides/image-generation`
	imagesGenerate.Flags = append(imagesGenerate.Flags, &cli.StringFlag{
		Name:        "output-dir",
		Usage:       "Save images to an existing `DIRECTORY` (also works in scripts)",
		DefaultText: "~/Downloads/gpt-images/ in a terminal",
	}, &cli.StringFlag{
		Name:        "inline",
		Usage:       "Inline preview `MODE`: on or off (overrides your saved preference)",
		Value:       "on",
		DefaultText: "saved preference, initially on",
	}, &cli.StringFlag{
		Name:  "name",
		Usage: "Save as `NAME` plus the image extension (for example, orange-robot)",
	}, &cli.BoolFlag{
		Name: "open", Usage: "Save and open the original in your default image viewer", HideDefault: true,
	}, &cli.BoolFlag{
		Name:        "no-preview",
		Usage:       "Save images without displaying terminal previews",
		HideDefault: true,
		Hidden:      true, // Keep the earlier opt-out working; prefer --inline off.
	})
	for _, flag := range imagesGenerate.Flags {
		switch flag := flag.(type) {
		case *requestflag.Flag[string]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*string]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[int64]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*int64]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*bool]:
			setImageFlagHelp(flag)
		}
	}
	// Put the everyday options first; keep all other and future flags visible.
	order := map[string]int{"prompt": 1, "model": 2, "open": 3, "inline": 4, "name": 5, "output-dir": 6, "size": 7, "n": 8, "quality": 9, "output-format": 10}
	rank := func(flag cli.Flag) int {
		if n := order[flag.Names()[0]]; n != 0 {
			return n
		}
		return len(order) + 1
	}
	sort.SliceStable(imagesGenerate.Flags, func(i, j int) bool {
		return rank(imagesGenerate.Flags[i]) < rank(imagesGenerate.Flags[j])
	})
}

type imageFlagHelp struct {
	usage, defaultText string
	hideDefault        bool
}

var imageGenerateHelp = map[string]imageFlagHelp{
	"prompt":             {usage: "Image description (required)."},
	"model":              {usage: "Image model; see saving defaults above.", hideDefault: true},
	"background":         {usage: "Background: auto, opaque, or transparent. Transparency needs PNG/WebP and model support."},
	"moderation":         {usage: "GPT image moderation: auto or low."},
	"n":                  {usage: "Number of images, from 1 to 10. DALL-E 3 supports only 1."},
	"output-compression": {usage: "JPEG/WebP compression level, from 0 to 100 (GPT image models)."},
	"output-format":      {usage: "Image file format: png, jpeg, or webp (GPT image models)."},
	"partial-images":     {usage: "Number of partial streaming images, from 0 to 3."},
	"quality":            {usage: "Image quality: auto, low, medium, or high. Other values depend on the model."},
	"response-format":    {usage: "DALL-E only: url (no saving) or b64_json.", hideDefault: true},
	"size":               {usage: "Image size, such as 1024x1024 or auto. Supported sizes depend on the model.", hideDefault: true},
	"stream":             {usage: "Stream generation events instead of saving images."},
	"style":              {usage: "DALL-E 3 style: vivid or natural."},
	"user":               {usage: "End-user identifier for abuse monitoring."},
	"max-items":          {usage: "Maximum streaming events (-1 for unlimited).", defaultText: "unlimited"},
}

func setImageFlagHelp[T any](flag *requestflag.Flag[T]) {
	if help, ok := imageGenerateHelp[flag.Name]; ok {
		flag.Usage = help.usage
		if help.defaultText != "" {
			flag.DefaultText = help.defaultText
		}
		if help.hideDefault {
			flag.HideDefault = true
		}
	}
}
