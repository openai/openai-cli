package imagefontmac

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// SourceFont is an exact installed face's unmodified sfnt tables plus its
// native metrics at the requested point size. Tables can include the optional
// OAIp lineage table when the source is one of our generated image fonts.
type SourceFont struct {
	PostScript string `json:"postscript"`
	// LookupName disambiguates bundled faces whose PostScript name is shared
	// across different font files. It is their exact native full face name.
	LookupName string `json:"lookup_name,omitempty"`
	// Variations captures the selected face's user-space axis coordinates.
	// Keys are decimal OpenType axis tags returned by CoreText.
	Variations  map[string]float64 `json:"variations,omitempty"`
	FamilyClass uint8              `json:"family_class,omitempty"`
	Style       string             `json:"style"`
	Tables      map[string][]byte  `json:"tables"`
	Ascent      float64            `json:"ascent"`
	Descent     float64            `json:"descent"`
	Leading     float64            `json:"leading"`
	Advance     float64            `json:"advance"`
	LineHeight  float64            `json:"line_height"`
	Companions  []SourceFont       `json:"companions,omitempty"`
}

//go:embed source.js
var sourceMainBridge string

//go:embed source_bundled.js
var sourceBundledBridge string

var sourceBridge = sourceBundledBridge + "\n" + sourceMainBridge

// ErrLegacyFont identifies an older gallery whose original text face was not
// recorded. Callers can retain that gallery's existing rendering behavior.
var ErrLegacyFont = errors.New("this older image font cannot identify your original font; select your original font and size in Terminal, then retry the command")

var ownedSourceName = regexp.MustCompile(`^OpenAIImages-[0-9a-f]{8}-[0-9a-f]{32}-(Regular|Bold|Italic|BoldItalic)$`)

// Source reads an exact installed face by PostScript or native display name without registering fonts,
// accessing application preferences, or controlling Terminal. Missing names
// fail rather than returning CoreText's automatic fallback font. For a generated
// image font, its OAIp lineage identifies the original installed face instead.
// Companions includes available real bold/italic faces from the same family.
func Source(ctx context.Context, postScript string, size int) (SourceFont, error) {
	return source(ctx, postScript, size, Supported, runSource)
}

func source(ctx context.Context, postScript string, size int, supported func() bool, execute runner) (SourceFont, error) {
	font, err := readSource(ctx, postScript, size, supported, execute)
	if err != nil || !strings.HasPrefix(font.PostScript, "OpenAIImages-") {
		return font, err
	}
	var origin struct {
		Version    int    `json:"version"`
		PostScript string `json:"source_postscript"`
		SourceName string `json:"source_name"`
	}
	lineage := font.Tables["OAIp"]
	if !ownedSourceName.MatchString(font.PostScript) || len(lineage) > 4096 || json.Unmarshal(lineage, &origin) != nil || origin.Version != 1 || !validSourceFontName(origin.PostScript) || strings.HasPrefix(origin.PostScript, "OpenAIImages-") || origin.SourceName != "" && (!validSourceFontName(origin.SourceName) || strings.HasPrefix(origin.SourceName, "OpenAIImages-")) {
		return SourceFont{}, ErrLegacyFont
	}
	lookup := origin.PostScript
	if origin.SourceName != "" {
		lookup = origin.SourceName
	}
	resolved, err := readSource(ctx, lookup, size, supported, execute)
	if err != nil {
		return SourceFont{}, err
	}
	if resolved.PostScript != origin.PostScript {
		return SourceFont{}, errors.New("the recorded original font resolved to a different face; restore your preferred font in Inspector, then retry the command")
	}
	return resolved, nil
}

func readSource(ctx context.Context, postScript string, size int, supported func() bool, execute runner) (SourceFont, error) {
	if err := ctx.Err(); err != nil {
		return SourceFont{}, err
	}
	if !supported() {
		return SourceFont{}, ErrUnsupported
	}
	if postScript == "" || len(postScript) > 255 || size <= 0 || size > 1024 {
		return SourceFont{}, errors.New("provide an installed font name and a size from 1 to 1024 points")
	}
	for _, r := range postScript {
		if unicode.IsControl(r) {
			return SourceFont{}, errors.New("font name cannot contain control characters")
		}
	}
	output, err := execute(ctx, interpreter, []string{"-l", "JavaScript", "-e", sourceBridge, postScript, strconv.Itoa(size)}, environment(os.Environ()))
	if ctx.Err() != nil {
		return SourceFont{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, errSourceOutputLimit) {
			return SourceFont{}, errSourceOutputLimit
		}
		return SourceFont{}, errors.New("read installed font: native bridge failed")
	}
	if len(output) > maxSourceOutputBytes {
		return SourceFont{}, errSourceOutputLimit
	}
	var result struct {
		OK          bool       `json:"ok"`
		Reason      string     `json:"reason"`
		MatchedName string     `json:"matched_name"`
		Font        SourceFont `json:"font"`
	}
	if json.Unmarshal(output, &result) != nil {
		return SourceFont{}, errors.New("installed-font bridge returned an invalid result")
	}
	if !result.OK {
		if result.Reason == "size" {
			return SourceFont{}, errors.New("installed font exceeds the supported export limits (64 MiB per face, four faces)")
		}
		if result.Reason == "missing" {
			return SourceFont{}, fmt.Errorf("the requested font %q is not available to this command; no fallback font was selected", postScript)
		}
		if result.Reason == "ambiguous" {
			return SourceFont{}, fmt.Errorf("the font name %q identifies more than one bundled typeface; no fallback font was selected", postScript)
		}
		return SourceFont{}, errors.New("could not read installed font tables and metrics")
	}
	font := result.Font
	// A differing canonical name is valid only when the native bridge positively
	// matched the exact requested display/full/family name on this same face.
	matched := font.PostScript == postScript && result.MatchedName == "" || font.PostScript != postScript && result.MatchedName == postScript
	if !matched {
		return SourceFont{}, errors.New("installed-font bridge returned mismatched or invalid font data")
	}
	if strings.HasPrefix(font.PostScript, "OpenAIImages-") {
		// Generated fonts return only their tiny lineage record. Reading their
		// metrics or sbix atlas before resolving the original face is unnecessary.
		if len(font.Companions) != 0 || len(font.Tables) > 1 || font.LookupName != "" || len(font.Variations) != 0 || font.FamilyClass != 0 {
			return SourceFont{}, errors.New("installed-font bridge returned invalid lineage data")
		}
		for tag := range font.Tables {
			if tag != "OAIp" {
				return SourceFont{}, errors.New("installed-font bridge returned invalid lineage data")
			}
		}
		return font, nil
	}
	valid := validSource(font) && len(font.Companions) < maxSourceFaces
	seen := map[string]bool{font.PostScript: true}
	styles := map[string]bool{font.Style: true}
	for _, companion := range font.Companions {
		valid = valid && !seen[companion.PostScript] && !styles[companion.Style] && len(companion.Companions) == 0 && validSource(companion)
		seen[companion.PostScript] = true
		styles[companion.Style] = true
	}
	if !valid {
		return SourceFont{}, errors.New("installed-font bridge returned mismatched or invalid font data")
	}
	return font, nil
}

func validSource(font SourceFont) bool {
	if !validSourceFontName(font.PostScript) || font.LookupName != "" && (!validSourceFontName(font.LookupName) || strings.HasPrefix(font.LookupName, "OpenAIImages-")) || len(font.Tables) == 0 || font.Ascent <= 0 || font.Descent < 0 || font.Advance <= 0 || font.LineHeight <= 0 || font.FamilyClass > 15 {
		return false
	}
	if font.Style != "Regular" && font.Style != "Bold" && font.Style != "Italic" && font.Style != "BoldItalic" {
		return false
	}
	for _, metric := range []float64{font.Ascent, font.Descent, font.Leading, font.Advance, font.LineHeight} {
		if math.IsNaN(metric) || math.IsInf(metric, 0) {
			return false
		}
	}
	for tag, coordinate := range font.Variations {
		if _, err := strconv.ParseUint(tag, 10, 32); err != nil || math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
			return false
		}
	}
	remaining := maxSourceFaceBytes
	if len(font.Tables) > 65535 || len(font.Variations) > 65535 {
		return false
	}
	for tag, data := range font.Tables {
		if len(tag) != 4 {
			return false
		}
		if len(data) > remaining {
			return false
		}
		remaining -= len(data)
	}
	return true
}

func validSourceFontName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
