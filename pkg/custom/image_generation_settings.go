package custom

import (
	"errors"
	"math"

	"github.com/tidwall/gjson"
)

// Validate documented constraints after flags, JSON/YAML stdin and file values
// have been merged, before preparing files or making a generation request.
// Omitted fields and explicit nulls retain their existing API semantics. Model,
// quality, size and other option strings stay open to future API additions.
func validateImageGenerationSettings(body gjson.Result) error {
	stream := body.Get("stream")
	if stream.Type != gjson.Null && stream.Type != gjson.True && stream.Type != gjson.False {
		return errors.New("--stream must be true or false; JSON/YAML input must use a boolean or null")
	}
	count := body.Get("n")
	if count.Type != gjson.Null {
		if !imageIntegerInRange(count, 1, 10) {
			return errors.New("--count (-n) must be a whole number from 1 to 10")
		}
		if body.Get("model").String() == "dall-e-3" && count.Float() != 1 {
			return errors.New("dall-e-3 supports exactly one image; use --count 1")
		}
	}
	partial := body.Get("partial_images")
	if partial.Type != gjson.Null {
		if !imageIntegerInRange(partial, 0, 3) {
			return errors.New("--partial-images must be a whole number from 0 to 3")
		}
	}
	// Positive partials select streaming in the CLI's saving workflow. Requiring
	// stream explicitly in API-data mode is handled after output mode selection.
	if (stream.Type == gjson.True || partial.Float() > 0) && count.Type != gjson.Null && count.Float() != 1 {
		return errors.New("streaming and partial images support exactly one image; use --count 1")
	}
	if body.Get("background").String() == "transparent" && body.Get("output_format").String() == "jpeg" {
		return errors.New("JPEG does not support transparent backgrounds; use --output-format png or --output-format webp, or --background opaque")
	}
	return nil
}

func imageIntegerInRange(value gjson.Result, minimum, maximum float64) bool {
	if value.Type != gjson.Number {
		return false
	}
	n := value.Float()
	return n >= minimum && n <= maximum && math.Trunc(n) == n
}
