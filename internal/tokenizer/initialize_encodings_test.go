package tokenizer

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dlclark/regexp2"
)

func TestEncodingVocabularyFingerprint(t *testing.T) {
	// Official tiktoken 0.14.0 public decode_single_token_bytes output.
	// Each entry hashes big-endian uint32(rank), uint32(length), then raw bytes.
	for _, tc := range []struct {
		name        string
		count       int
		maximum     int
		fingerprint string
		specials    []uint
	}{
		{"cl100k_base", 100256, 100255, "9291e9f4743a26dc09f63cf6ca43abca3b6ee7f6dc58bd7bd2d2ad8dfdc201d9", []uint{100256, 100257, 100258, 100259, 100260, 100276}},
		{"o200k_base", 199998, 199997, "dcf8f06c59a061f59909285da2f0fbbc0fa772916ad5f0b4ed52ce6a1c32d04a", []uint{199998, 199999, 200018}},
		{"r50k_base", 50256, 50255, "b8b450861649b2f643a5038549e1a302d70e36023b75046611725e18a031672a", []uint{50256, 50257}},
		{"p50k_base", 50280, 50280, "0b9d4d17e726ec6fe3a6dbdf94ee8f17c8cb8b18a00f457f8f07e6067b4245a5", []uint{50256, 50281}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := loadEncoding(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if len(enc.ranks) != tc.count || len(enc.fragments) != tc.maximum+1 {
				t.Fatal("ordinary vocabulary size changed")
			}
			hash := sha256.New()
			var header [8]byte
			for id, fragment := range enc.fragments {
				if tc.name == "p50k_base" && id == 50256 && fragment == "" {
					continue
				}
				if fragment == "" || enc.ranks[fragment] != id {
					t.Fatalf("invalid ordinary rank %d", id)
				}
				binary.BigEndian.PutUint32(header[:4], uint32(id))
				binary.BigEndian.PutUint32(header[4:], uint32(len(fragment)))
				hash.Write(header[:])
				hash.Write([]byte(fragment))
			}
			if got := fmt.Sprintf("%x", hash.Sum(nil)); got != tc.fingerprint {
				t.Fatalf("ordinary vocabulary fingerprint = %s", got)
			}
			for _, rank := range tc.specials {
				if rank < uint(len(enc.fragments)) && enc.fragments[rank] != "" {
					t.Fatalf("special or gap rank %d entered ordinary vocabulary", rank)
				}
			}
			for _, spelling := range []string{"<|endoftext|>", "<|fim_prefix|>", "<|endofprompt|>"} {
				result, err := enc.encode(spelling, tc.name, true)
				if err != nil || strings.Join(result.Fragments, "") != spelling {
					t.Fatalf("ordinary marker failed: %v", err)
				}
				for _, rank := range result.IDs {
					if rank >= uint(len(enc.fragments)) || enc.fragments[rank] == "" {
						t.Fatalf("special rank %d returned", rank)
					}
				}
			}
		})
	}
}

func TestEncodingConcurrentColdInitialization(t *testing.T) {
	for _, name := range SupportedEncodings() {
		t.Run(name, func(t *testing.T) {
			var cache encodingCache
			start := make(chan struct{})
			type observation struct {
				encoding *textEncoding
				ids      []uint
				err      error
			}
			results := make(chan observation, 8)
			var workers sync.WaitGroup
			for i := 0; i < 8; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					enc, err := cache.load(name)
					var ids []uint
					if err == nil {
						// This unranked piece also exercises concurrent lazy BPE creation.
						result, resultErr := enc.encode("\U00016ea0\\u", name, true)
						err, ids = resultErr, result.IDs
						if err == nil && strings.Join(result.Fragments, "") != "\U00016ea0\\u" {
							err = fmt.Errorf("source bytes changed")
						}
					}
					results <- observation{enc, ids, err}
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			var first *textEncoding
			var ids []uint
			for got := range results {
				if got.err != nil {
					t.Fatal(got.err)
				}
				if first == nil {
					first, ids = got.encoding, got.ids
				}
				if got.encoding != first || !reflect.DeepEqual(got.ids, ids) {
					t.Fatal("concurrent initialization changed encoding identity or tokens")
				}
			}
		})
	}
}

func TestEncodingRankedPiecesKeepBPELazy(t *testing.T) {
	for _, name := range SupportedEncodings() {
		t.Run(name, func(t *testing.T) {
			enc, err := initializeEncoding(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, inspect := range []bool{false, true} {
				result, err := enc.encode("a1\n", name, inspect)
				if err != nil || result.TokenCount != 3 {
					t.Fatalf("ranked pieces: %#v, %v", result, err)
				}
				if enc.bpe != nil {
					t.Fatal("ranked pieces initialized the BPE merge engine")
				}
			}
		})
	}
}

func TestSupportedEncodingNames(t *testing.T) {
	want := []string{"o200k_base", "cl100k_base", "r50k_base", "p50k_base"}
	if DefaultEncoding != want[0] || !reflect.DeepEqual(SupportedEncodings(), want) {
		t.Fatal("encoding names, order, or default changed")
	}
	changed := SupportedEncodings()
	changed[0] = "changed"
	if !reflect.DeepEqual(SupportedEncodings(), want) {
		t.Fatal("caller changed the supported encoding list")
	}
	for _, name := range want {
		if !IsSupportedEncoding(name) {
			t.Fatalf("supported encoding %q rejected", name)
		}
	}
	for _, name := range []string{"", "P50K_BASE", "p50k_base ", "gpt2", "davinci", "p50k_edit"} {
		if IsSupportedEncoding(name) {
			t.Fatalf("nonexact encoding %q accepted", name)
		}
	}
}

func TestEncodingRejectsIncompleteResultsInBothModes(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, text string
		rank                int
		fragments           []string
	}{
		{"suffix", "a", "ab", 0, []string{"a"}},
		{"prefix", "b", "ab", 0, []string{"b"}},
		{"wrong bytes", "a", "a", 0, []string{"b"}},
		{"empty fragment", "a", "a", 0, []string{""}},
		{"invalid ID", "a", "a", 1, []string{"a"}},
		{"negative ID", "a", "a", -1, []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := &textEncoding{split: regexp2.MustCompile(tc.pattern, regexp2.None), ranks: map[string]int{tc.pattern: tc.rank}, fragments: tc.fragments}
			for _, inspect := range []bool{false, true} {
				result, err := enc.encode(tc.text, "cl100k_base", inspect)
				if err == nil || err.Error() != "tokenizer could not preserve input bytes" || !reflect.DeepEqual(result, Result{}) {
					t.Fatalf("accepted incomplete result: %#v, %v", result, err)
				}
			}
		})
	}
}

func TestEncodingPatternsHaveNoHostUnicodeRules(t *testing.T) {
	for _, name := range SupportedEncodings() {
		pattern, err := encodingPattern(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, shorthand := range []string{`\p`, `\P`, `\s`, `\S`, `\w`, `\W`, `\d`, `\D`, "(?i"} {
			if strings.Contains(pattern, shorthand) {
				t.Fatalf("%s retained compiler-dependent %s", name, shorthand)
			}
		}
	}
}
