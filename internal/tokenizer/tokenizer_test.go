package tokenizer

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// IDs and bytes come from OpenAI tiktoken 0.14.0 encode_ordinary.
// See https://github.com/openai/tiktoken/tree/0.14.0.
// The control-byte and whitespace cases detect regressions in tokenizer v0.8.1.
func TestEncodeReferenceVectors(t *testing.T) {
	tests := []struct {
		encoding string
		text     string
		ids      []uint
		hex      []string
	}{
		{"cl100k_base", "", []uint{}, []string{}},
		{"cl100k_base", "hello world", []uint{15339, 1917}, []string{"68656c6c6f", "20776f726c64"}},
		{"cl100k_base", "Hello, world!", []uint{9906, 11, 1917, 0}, []string{"48656c6c6f", "2c", "20776f726c64", "21"}},
		{"cl100k_base", "today\n \n", []uint{31213, 27907}, []string{"746f646179", "0a200a"}},
		{"cl100k_base", "today\n  \n", []uint{31213, 14211}, []string{"746f646179", "0a20200a"}},
		{"cl100k_base", "\x00\x1b\r\n\t", []uint{188, 215, 221, 319, 197}, []string{"00", "1b", "7f", "0d0a", "09"}},
		{"cl100k_base", "I'M WE'RE THEY'VE IT'S YOU'LL I'D ISN'T", []uint{40, 28703, 20255, 95253, 63593, 6, 4592, 8871, 13575, 15334, 6, 4178, 358, 28805, 3507, 45, 17773}, []string{"49", "274d", "205745", "275245", "2054484559", "27", "5645", "204954", "2753", "20594f55", "27", "4c4c", "2049", "2744", "204953", "4e", "2754"}},
		{"cl100k_base", "rer 'rer", []uint{38149, 364, 38149}, []string{"726572", "2027", "726572"}},
		{"cl100k_base", "你好，世界！", []uint{57668, 53901, 3922, 3574, 244, 98220, 6447}, []string{"e4bda0", "e5a5bd", "efbc8c", "e4b8", "96", "e7958c", "efbc81"}},
		{"cl100k_base", "こんにちは世界", []uint{90115, 3574, 244, 98220}, []string{"e38193e38293e381abe381a1e381af", "e4b8", "96", "e7958c"}},
		{"cl100k_base", "مرحبا بالعالم", []uint{10386, 11318, 30925, 22071, 5821, 28946, 32482, 24102, 32482, 10386}, []string{"d985", "d8b1", "d8ad", "d8a8", "d8a7", "20d8a8", "d8a7d984", "d8b9", "d8a7d984", "d985"}},
		{"cl100k_base", "नमस्ते दुनिया", []uint{61196, 88344, 79468, 31584, 97, 35470, 15272, 99, 73753, 61196, 43411, 107, 24810}, []string{"e0a4a8", "e0a4ae", "e0a4b8", "e0a58de0a4", "a4", "e0a587", "20e0a4", "a6", "e0a581", "e0a4a8", "e0a4bfe0a4", "af", "e0a4be"}},
		{"cl100k_base", "👍", []uint{9468, 239, 235}, []string{"f09f", "91", "8d"}},
		{"cl100k_base", "👩‍💻", []uint{9468, 239, 102, 378, 235, 93273, 119}, []string{"f09f", "91", "a9", "e280", "8d", "f09f92", "bb"}},
		{"cl100k_base", "👨‍👩‍👧‍👦", []uint{9468, 239, 101, 378, 235, 9468, 239, 102, 378, 235, 9468, 239, 100, 378, 235, 9468, 239, 99}, []string{"f09f", "91", "a8", "e280", "8d", "f09f", "91", "a9", "e280", "8d", "f09f", "91", "a7", "e280", "8d", "f09f", "91", "a6"}},
		{"cl100k_base", "á", []uint{64, 54939}, []string{"61", "cc81"}},
		{"cl100k_base", "é", []uint{978}, []string{"c3a9"}},
		{"cl100k_base", "é", []uint{68, 54939}, []string{"65", "cc81"}},
		{"cl100k_base", "\ufeffhello", []uint{3305, 15339}, []string{"efbbbf", "68656c6c6f"}},
		{"cl100k_base", "<|endoftext|><|fim_prefix|><|start|>", []uint{27, 91, 8862, 728, 428, 91, 1822, 91, 69, 318, 14301, 91, 1822, 91, 2527, 91, 29}, []string{"3c", "7c", "656e646f", "6674", "657874", "7c", "3e3c", "7c", "66", "696d", "5f707265666978", "7c", "3e3c", "7c", "7374617274", "7c", "3e"}},
		{"cl100k_base", "'RE", []uint{95253}, []string{"275245"}},
		{"cl100k_base", "'re", []uint{2351}, []string{"277265"}},
		{"cl100k_base", "'Re", []uint{50527}, []string{"275265"}},
		{"cl100k_base", "'rE", []uint{97670, 36}, []string{"2772", "45"}},
		{"cl100k_base", "x'RE", []uint{87, 95253}, []string{"78", "275245"}},
		{"cl100k_base", "hello'RE", []uint{15339, 95253}, []string{"68656c6c6f", "275245"}},
		{"o200k_base", "", []uint{}, []string{}},
		{"o200k_base", "hello world", []uint{24912, 2375}, []string{"68656c6c6f", "20776f726c64"}},
		{"o200k_base", "Hello, world!", []uint{13225, 11, 2375, 0}, []string{"48656c6c6f", "2c", "20776f726c64", "21"}},
		{"o200k_base", "today\n \n", []uint{58744, 47812}, []string{"746f646179", "0a200a"}},
		{"o200k_base", "today\n  \n", []uint{58744, 31835}, []string{"746f646179", "0a20200a"}},
		{"o200k_base", "\x00\x1b\r\n\t", []uint{188, 215, 221, 370, 197}, []string{"00", "1b", "7f", "0d0a", "09"}},
		{"o200k_base", "I'M WE'RE THEY'VE IT'S YOU'LL I'D ISN'T", []uint{40, 95346, 26919, 6, 1099, 95381, 6, 19511, 8734, 31233, 19461, 6, 7454, 3413, 35, 5436, 45, 51532}, []string{"49", "274d", "205745", "27", "5245", "2054484559", "27", "5645", "204954", "2753", "20594f55", "27", "4c4c", "204927", "44", "204953", "4e", "2754"}},
		{"o200k_base", "rer 'rer", []uint{27653, 461, 27653}, []string{"726572", "2027", "726572"}},
		{"o200k_base", "你好，世界！", []uint{177519, 979, 28428, 3393}, []string{"e4bda0e5a5bd", "efbc8c", "e4b896e7958c", "efbc81"}},
		{"o200k_base", "こんにちは世界", []uint{95839, 28428}, []string{"e38193e38293e381abe381a1e381af", "e4b896e7958c"}},
		{"o200k_base", "مرحبا بالعالم", []uint{158894, 26537, 101462, 12773}, []string{"d985d8b1d8ad", "d8a8d8a7", "20d8a8d8a7d984d8b9", "d8a7d984d985"}},
		{"o200k_base", "नमस्ते दुनिया", []uint{998, 1637, 14681, 628, 64593}, []string{"e0a4a8", "e0a4ae", "e0a4b8e0a58de0a4a4", "e0a587", "20e0a4a6e0a581e0a4a8e0a4bfe0a4afe0a4be"}},
		{"o200k_base", "👍", []uint{82514}, []string{"f09f918d"}},
		{"o200k_base", "👩‍💻", []uint{28823, 102, 2524, 31446, 119}, []string{"f09f91", "a9", "e2808d", "f09f92", "bb"}},
		{"o200k_base", "👨‍👩‍👧‍👦", []uint{28823, 101, 2524, 28823, 102, 2524, 28823, 100, 2524, 28823, 99}, []string{"f09f91", "a8", "e2808d", "f09f91", "a9", "e2808d", "f09f91", "a7", "e2808d", "f09f91", "a6"}},
		{"o200k_base", "á", []uint{64, 13430}, []string{"61", "cc81"}},
		{"o200k_base", "é", []uint{377}, []string{"c3a9"}},
		{"o200k_base", "é", []uint{68, 13430}, []string{"65", "cc81"}},
		{"o200k_base", "\ufeffhello", []uint{5574, 24912}, []string{"efbbbf", "68656c6c6f"}},
		{"o200k_base", "<|endoftext|><|fim_prefix|><|start|>", []uint{27, 91, 419, 1440, 919, 91, 3784, 91, 103473, 33197, 91, 3784, 91, 5236, 91, 29}, []string{"3c", "7c", "656e64", "6f66", "74657874", "7c", "3e3c", "7c", "66696d", "5f707265666978", "7c", "3e3c", "7c", "7374617274", "7c", "3e"}},
		{"o200k_base", "'RE", []uint{6, 1099}, []string{"27", "5245"}},
		{"o200k_base", "'re", []uint{4118}, []string{"277265"}},
		{"o200k_base", "'Re", []uint{146756}, []string{"275265"}},
		{"o200k_base", "'rE", []uint{15770, 36}, []string{"2772", "45"}},
		{"o200k_base", "x'RE", []uint{87, 6, 1099}, []string{"78", "27", "5245"}},
		{"o200k_base", "hello'RE", []uint{24912, 6, 1099}, []string{"68656c6c6f", "27", "5245"}},
	}

	for _, tt := range tests {
		t.Run(tt.encoding+"/"+hex.EncodeToString([]byte(tt.text)), func(t *testing.T) {
			result, err := Encode(tt.text, tt.encoding, true)
			if err != nil {
				t.Fatal(err)
			}
			if result.Encoding != tt.encoding || result.InputBytes != len(tt.text) || result.TokenCount != len(tt.ids) {
				t.Fatalf("incorrect metadata: %#v", result)
			}
			if !reflect.DeepEqual(result.IDs, tt.ids) {
				t.Fatalf("IDs = %v; want %v", result.IDs, tt.ids)
			}
			fragments := make([]string, len(result.Fragments))
			for i, fragment := range result.Fragments {
				fragments[i] = hex.EncodeToString([]byte(fragment))
			}
			if !reflect.DeepEqual(fragments, tt.hex) {
				t.Fatalf("fragment bytes = %v; want %v", fragments, tt.hex)
			}
			if strings.Join(result.Fragments, "") != tt.text {
				t.Fatal("fragments changed input bytes")
			}
			count, err := Encode(tt.text, tt.encoding, false)
			if err != nil || count.TokenCount != result.TokenCount {
				t.Fatalf("count = %#v, %v; want %d", count, err, result.TokenCount)
			}
			if count.IDs != nil || count.Fragments != nil {
				t.Fatal("count retained token inspection data")
			}
		})
	}
}

func TestEncodeInvalidInput(t *testing.T) {
	for _, tt := range []struct{ name, text, encoding, want string }{
		{"encoding", "hello", "unknown", "unsupported encoding; choose o200k_base or cl100k_base"},
		{"missing encoding", "hello", "", "unsupported encoding; choose o200k_base or cl100k_base"},
		{"invalid UTF-8", string([]byte{0xff}), "o200k_base", "tokenizer input must be valid UTF-8"},
		{"truncated UTF-8", string([]byte{0xf0, 0x9f}), "cl100k_base", "tokenizer input must be valid UTF-8"},
		{"input limit", strings.Repeat("x", MaxInputBytes+1), "o200k_base", "tokenizer input exceeds the 1 MiB limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, inspect := range []bool{false, true} {
				result, err := Encode(tt.text, tt.encoding, inspect)
				if err == nil || err.Error() != tt.want {
					t.Fatalf("error = %v; want %q", err, tt.want)
				}
				if !reflect.DeepEqual(result, Result{}) {
					t.Fatal("invalid input returned a partial result")
				}
			}
		})
	}
}

func TestEncodeUnicodeFragmentsRemainLossless(t *testing.T) {
	result, err := Encode("👍", "cl100k_base", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range result.Fragments {
		if utf8.ValidString(fragment) {
			t.Fatalf("expected partial UTF-8 bytes, got %x", fragment)
		}
	}
	if strings.Join(result.Fragments, "") != "👍" {
		t.Fatal("partial UTF-8 bytes did not reconstruct the input")
	}
}

func TestEncodeLongInputAndBoundary(t *testing.T) {
	for _, encoding := range []string{"cl100k_base", "o200k_base"} {
		t.Run(encoding, func(t *testing.T) {
			// This exact 1 MiB input produces one digit and one newline token per pair.
			text := strings.Repeat("0\n", MaxInputBytes/2)
			count, err := Encode(text, encoding, false)
			if err != nil || count.TokenCount != MaxInputBytes || count.InputBytes != MaxInputBytes {
				t.Fatalf("maximum input count = %#v, %v", count, err)
			}
			text = strings.Repeat("hello world\n", 4096)
			first, err := Encode(text, encoding, true)
			if err != nil || first.TokenCount != 3*4096 || strings.Join(first.Fragments, "") != text {
				t.Fatalf("long prose: count=%d, error=%v", first.TokenCount, err)
			}
			second, err := Encode(text, encoding, true)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatal("repeated tokenization changed the result")
			}
		})
	}
}
