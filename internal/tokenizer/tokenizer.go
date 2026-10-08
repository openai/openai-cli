// Package tokenizer adapts embedded text encodings for local CLI token inspection.
package tokenizer

import (
	_ "embed"
	"errors"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer/codec"
)

// MaxInputBytes bounds this local utility's input and result allocations.
// It does not limit API requests or guarantee short execution for unbroken text.
const MaxInputBytes = 1 << 20

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

	var encoder *codec.Codec
	switch encoding {
	case "cl100k_base":
		encoder = codec.NewCl100kBase()
	case "o200k_base":
		encoder = codec.NewO200kBase()
	default:
		return Result{}, errors.New("unsupported encoding; choose o200k_base or cl100k_base")
	}

	result := Result{Encoding: encoding, InputBytes: len(text)}
	if !inspect {
		count, err := encoder.Count(text)
		if err != nil {
			return Result{}, errors.New("could not tokenize input")
		}
		result.TokenCount = count
		return result, nil
	}

	ids, fragments, err := encoder.Encode(text)
	if err != nil {
		return Result{}, errors.New("could not tokenize input")
	}
	if len(ids) != len(fragments) {
		return Result{}, errors.New("tokenizer could not preserve input bytes")
	}
	offset := 0
	for _, fragment := range fragments {
		if len(fragment) == 0 || len(fragment) > len(text)-offset || text[offset:offset+len(fragment)] != fragment {
			return Result{}, errors.New("tokenizer could not preserve input bytes")
		}
		offset += len(fragment)
	}
	if offset != len(text) {
		return Result{}, errors.New("tokenizer could not preserve input bytes")
	}
	if ids == nil {
		ids = []uint{}
		fragments = []string{}
	}
	result.IDs = ids
	result.Fragments = fragments
	result.TokenCount = len(ids)
	return result, nil
}
