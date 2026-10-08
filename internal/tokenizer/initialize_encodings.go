package tokenizer

import (
	"errors"
	"fmt"
	"sync"

	"github.com/dlclark/regexp2"
	bpe "github.com/pkoukk/tiktoken-go"
	"github.com/tiktoken-go/tokenizer/codec"
)

const unsupportedEncodingMessage = "unsupported encoding; choose o200k_base or cl100k_base"

// textEncoding owns immutable vocabulary data and the pinned pre-tokenizer.
// Library BPE construction is lazy because exact ranked pieces need no merge.
type textEncoding struct {
	split     *regexp2.Regexp
	ranks     map[string]int
	fragments []string
	bpeOnce   sync.Once
	bpe       *bpe.Tiktoken
	bpeErr    error
}

type encodingCache struct {
	once  sync.Once
	value *textEncoding
	err   error
}

var cl100kEncoding, o200kEncoding encodingCache

func loadEncoding(name string) (*textEncoding, error) {
	switch name {
	case "cl100k_base":
		return cl100kEncoding.load(name)
	case "o200k_base":
		return o200kEncoding.load(name)
	default:
		return nil, errors.New(unsupportedEncodingMessage)
	}
}

func (c *encodingCache) load(name string) (*textEncoding, error) {
	c.once.Do(func() { c.value, c.err = initializeEncoding(name) })
	return c.value, c.err
}

func initializeEncoding(name string) (*textEncoding, error) {
	var source *codec.Codec
	var maximum int
	switch name {
	case "cl100k_base":
		source, maximum = codec.NewCl100kBase(), 100255
	case "o200k_base":
		source, maximum = codec.NewO200kBase(), 199997
	default:
		return nil, errors.New(unsupportedEncodingMessage)
	}

	ranks := make(map[string]int, maximum+1)
	fragments := make([]string, maximum+1)
	// Decode builds a mutable reverse map internally. Extract serially through
	// its public API, then discard the source codec before publishing our data.
	for id := 0; id <= maximum; id++ {
		piece, err := source.Decode([]uint{uint(id)})
		if err != nil {
			return nil, fmt.Errorf("invalid embedded token %d: %w", id, err)
		}
		if piece == "" {
			return nil, fmt.Errorf("empty embedded token %d", id)
		}
		if _, duplicate := ranks[piece]; duplicate {
			return nil, fmt.Errorf("duplicate embedded token %d", id)
		}
		ranks[piece], fragments[id] = id, piece
	}
	for _, id := range []uint{uint(maximum + 1), uint(maximum + 2), ^uint(0)} {
		if _, err := source.Decode([]uint{id}); err == nil {
			return nil, fmt.Errorf("unexpected embedded token %d", id)
		}
	}
	for value := 0; value < 256; value++ {
		if _, ok := ranks[string([]byte{byte(value)})]; !ok {
			return nil, fmt.Errorf("missing embedded byte token %d", value)
		}
	}
	pattern, err := encodingPattern(name)
	if err != nil {
		return nil, err
	}
	split, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil {
		return nil, err
	}
	return &textEncoding{split: split, ranks: ranks, fragments: fragments}, nil
}

func (e *textEncoding) encodePiece(piece string) ([]int, error) {
	e.bpeOnce.Do(func() {
		// The outer matcher already chose one complete reference pre-tokenizer
		// piece. The maintained library performs BPE without splitting it again.
		core, err := bpe.NewCoreBPE(e.ranks, map[string]int{}, `(?s:\A.+\z)`)
		if err != nil {
			e.bpeErr = err
			return
		}
		e.bpe = bpe.NewTiktoken(core, nil, nil)
	})
	if e.bpeErr != nil {
		return nil, e.bpeErr
	}
	// Ordinary encoding never activates special markers, loaders, or downloads.
	return e.bpe.EncodeOrdinary(piece), nil
}

func encodingPattern(name string) (string, error) {
	// Unicode16 simple-casefold closures for the reference's contraction letters.
	// Explicit classes avoid regexp2's compiler-dependent Unicode case folding.
	contraction := `'(?:[sSſ]|[tT]|[rR][eE]|[vV][eE]|[mM]|[lL][lL]|[dD])`
	letters, numbers := "["+unicode16Letters+"]", "["+unicode16Numbers+"]"
	space, notSpace := "["+unicode16Whitespace+"]", "[^"+unicode16Whitespace+"]"
	other := "[^" + unicode16Whitespace + unicode16Letters + unicode16Numbers + "]"
	prefix := `[^\r\n` + unicode16Letters + unicode16Numbers + `]`
	suffix := space + `*[\r\n]+|` + space + `+(?!` + notSpace + `)|` + space + `+`
	switch name {
	case "cl100k_base":
		return contraction + `|` + prefix + `?` + letters + `+|` + numbers + `{1,3}| ?` + other + `+[\r\n]*|` + suffix, nil
	case "o200k_base":
		upper, lower := "["+unicode16Upper+"]", "["+unicode16Lower+"]"
		return prefix + `?` + upper + `*` + lower + `+(?:` + contraction + `)?|` +
			prefix + `?` + upper + `+` + lower + `*(?:` + contraction + `)?|` +
			numbers + `{1,3}| ?` + other + `+[\r\n/]*|` + suffix, nil
	default:
		return "", errors.New(unsupportedEncodingMessage)
	}
}
