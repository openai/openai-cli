package transformers

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// ProjectAudioResponse separates a transcript from its metadata for readable
// presentation. Native text and subtitle bodies reach this boundary as strings.
// The original value remains available to every explicit data format.
func ProjectAudioResponse(value gjson.Result, route Route) (readable.StreamEvent, bool) {
	if route.OutputKind != OutputResponse ||
		(route.Operation != "(resource) audio.transcriptions > (method) create" &&
			route.Operation != "(resource) audio.translations > (method) create") || !gjson.Valid(value.Raw) {
		return readable.StreamEvent{}, false
	}
	text := value
	var details gjson.Result
	if value.IsObject() {
		if value.Get("type").Exists() || value.Get("object").Exists() ||
			value.Get("error").Exists() && value.Get("error").Type != gjson.Null {
			return readable.StreamEvent{}, false
		}
		if status := value.Get("status"); status.Exists() && status.Type != gjson.Null &&
			(status.Type != gjson.String || status.Str != "completed") {
			return readable.StreamEvent{}, false
		}
		text = value.Get("text")
	}
	if text.Type != gjson.String {
		return readable.StreamEvent{}, false
	}
	if value.IsObject() {
		if event, ok := projectTranscriptSegments(value, text.Str); ok {
			return event, true
		}
		details = streamResidual(value, nil, "text")
	}
	return readable.StreamEvent{
		Parts:   []readable.StreamPart{{Key: "transcript", Text: text.Str, Snapshot: true}},
		Details: details,
	}, true
}

// Only complete arrays of text segments can replace the ordinary transcript.
// Unknown shapes retain the original readable fallback without dropping fields.
func projectTranscriptSegments(value gjson.Result, transcript string) (readable.StreamEvent, bool) {
	segments := value.Get("segments")
	if !segments.IsArray() {
		return readable.StreamEvent{}, false
	}
	var lines strings.Builder
	var residuals []gjson.Result
	remaining := strings.Trim(transcript, " \n\t")
	matched, valid := true, true
	segments.ForEach(func(_, segment gjson.Result) bool {
		text := segment.Get("text")
		if !segment.IsObject() || text.Type != gjson.String {
			valid = false
			return false
		}
		seen := map[string]bool{}
		segment.ForEach(func(key, _ gjson.Result) bool {
			switch key.Str {
			case "text", "speaker", "start", "end":
				if seen[key.Str] {
					valid = false
				}
				seen[key.Str] = true
			}
			return valid
		})
		if !valid {
			return false
		}
		line := strings.Trim(text.Str, " \n\t")
		if matched {
			if strings.HasPrefix(remaining, line) {
				remaining = strings.TrimLeft(remaining[len(line):], " \n\t")
			} else {
				matched = false
			}
		}
		if len(residuals) > 0 {
			lines.WriteByte('\n')
		}
		omitted := []string{"text"}
		start, end := segment.Get("start"), segment.Get("end")
		startLabel, startNumber := transcriptTimestamp(start)
		endLabel, endNumber := transcriptTimestamp(end)
		if startNumber != nil && endNumber != nil && endNumber.Cmp(startNumber) >= 0 {
			lines.WriteString("[" + startLabel + "–" + endLabel + "] ")
			// Keep precision that the millisecond labels cannot represent.
			if new(big.Rat).Mul(startNumber, big.NewRat(1000, 1)).IsInt() {
				omitted = append(omitted, "start")
			}
			if new(big.Rat).Mul(endNumber, big.NewRat(1000, 1)).IsInt() {
				omitted = append(omitted, "end")
			}
		}
		if speaker := segment.Get("speaker"); speaker.Type == gjson.String && speaker.Str != "" {
			lines.WriteString(speaker.Str + ": ")
			omitted = append(omitted, "speaker")
		}
		lines.WriteString(line)
		residuals = append(residuals, streamResidual(segment, nil, omitted...))
		return true
	})
	if !valid || len(residuals) == 0 {
		return readable.StreamEvent{}, false
	}
	part := readable.StreamPart{Key: "transcript", Text: lines.String(), Snapshot: true}
	event := readable.StreamEvent{}
	if !matched || remaining != "" {
		// Revised or incomplete segment text cannot stand in for the aggregate.
		event.Parts = append(event.Parts, readable.StreamPart{Key: "transcript", Text: transcript, Snapshot: true})
		part.Key, part.Label = "transcript:segments", "Segments"
	}
	event.Parts = append(event.Parts, part)
	event.Details = streamResidual(value, map[string]gjson.Result{"segments": streamResidualArray(residuals)}, "text")
	return event, true
}

// Timestamps are presentation labels, not new bounds on accepted API payloads.
// Values outside exact float64 millisecond arithmetic retain their original data.
func transcriptTimestamp(value gjson.Result) (string, *big.Rat) {
	if value.Type != gjson.Number || math.IsNaN(value.Num) || math.IsInf(value.Num, 0) || value.Num < 0 {
		return "", nil
	}
	millis := math.Round(value.Num * 1000)
	if millis > 1<<53-1 || len(value.Raw) > 64 {
		return "", nil
	}
	// Bound only optional exact-number formatting work. Extreme representations
	// remain visible verbatim in metadata, without rejecting any response.
	if index := strings.IndexAny(value.Raw, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(value.Raw[index+1:])
		if err != nil || exponent < -400 || exponent > 400 {
			return "", nil
		}
	}
	number, ok := new(big.Rat).SetString(value.Raw)
	if !ok || number.Sign() < 0 {
		return "", nil
	}
	scaled := new(big.Rat).Mul(number, big.NewRat(1000, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	if remainder.Mul(remainder, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	total := whole.Int64()
	hours, minutes, seconds := total/3600000, total/60000%60, total/1000%60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, total%1000), number
	}
	return fmt.Sprintf("%02d:%02d.%03d", minutes, seconds, total%1000), number
}
