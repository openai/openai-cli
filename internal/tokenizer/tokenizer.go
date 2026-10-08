// Package tokenizer adapts embedded text encodings for local CLI token inspection.
package tokenizer

import (
	_ "embed"
	"errors"
	"unicode/utf8"
)

// MaxInputBytes bounds this local utility's input and result allocations.
// It does not limit API requests or guarantee short execution for unbroken text.
const MaxInputBytes = 1 << 20

// DefaultEncoding is the encoding used when the caller makes no selection.
const DefaultEncoding = "o200k_base"

// SupportedEncodings returns exact encoding names in presentation order.
func SupportedEncodings() []string {
	names := make([]string, len(encodingDefinitions))
	for i := range encodingDefinitions {
		names[i] = encodingDefinitions[i].name
	}
	return names
}

// IsSupportedEncoding checks an exact encoding name without loading vocabulary data.
func IsSupportedEncoding(name string) bool {
	return encodingDefinitionFor(name) != nil
}

// Licenses retains the tokenizer dependencies' distribution notices in the binary.
//
//go:embed LICENSES.txt
var Licenses string

// Result preserves the encoding's exact token IDs and byte fragments when inspected.
// A fragment can contain only part of a UTF-8 character.
type Result struct {
	Encoding   string
	InputBytes int
	TokenCount int
	IDs        []uint
	Fragments  []string
}

// Encode treats special-token markers as ordinary text and preserves input bytes.
// It does not normalize Unicode, strip a BOM, or modify whitespace.
// Calls are synchronous. The dependency does not expose context cancellation.
func Encode(text, encoding string, inspect bool) (Result, error) {
	if len(text) > MaxInputBytes {
		return Result{}, errors.New("tokenizer input exceeds the 1 MiB limit")
	}
	if !utf8.ValidString(text) {
		return Result{}, errors.New("tokenizer input must be valid UTF-8")
	}
	encoder, err := loadEncoding(encoding)
	if err != nil {
		return Result{}, err
	}
	return encoder.encode(text, encoding, inspect)
}

func (e *textEncoding) encode(text, name string, inspect bool) (Result, error) {
	result := Result{Encoding: name, InputBytes: len(text)}
	if inspect {
		result.IDs, result.Fragments = []uint{}, []string{}
	}
	offset := 0
	yield := func(id int) bool {
		if id < 0 || id >= len(e.fragments) {
			return false
		}
		fragment := e.fragments[id]
		if fragment == "" || len(fragment) > len(text)-offset || text[offset:offset+len(fragment)] != fragment {
			return false
		}
		offset += len(fragment)
		result.TokenCount++
		if inspect {
			result.IDs = append(result.IDs, uint(id))
			result.Fragments = append(result.Fragments, fragment)
		}
		return true
	}
	// Iterate exact reference pieces. Count never retains a whole-input token
	// list. This is reference pre-tokenization, not arbitrary input chunking.
	match, err := e.split.FindStringMatch(text)
	for err == nil && match != nil {
		piece := match.String()
		if id, ranked := e.ranks[piece]; ranked {
			if !yield(id) {
				return Result{}, errors.New("tokenizer could not preserve input bytes")
			}
		} else {
			ids, err := e.encodePiece(piece)
			if err != nil {
				return Result{}, errors.New("could not tokenize input")
			}
			for _, id := range ids {
				if !yield(id) {
					return Result{}, errors.New("tokenizer could not preserve input bytes")
				}
			}
		}
		match, err = e.split.FindNextMatch(match)
	}
	if err != nil {
		return Result{}, errors.New("could not tokenize input")
	}
	// The BPE dependency ignores regex errors internally. Both modes reject
	// partial results, including any unmatched outer-regex suffix or gap.
	if offset != len(text) {
		return Result{}, errors.New("tokenizer could not preserve input bytes")
	}
	return result, nil
}
