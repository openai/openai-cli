package custom

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestImageSettingsValidation(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
	}{
		{"omitted", `{}`, ""},
		{"explicit nulls", `{"n":null,"partial_images":null,"stream":null,"background":null,"output_format":null}`, ""},
		{"stream false", `{"stream":false}`, ""},
		{"stream true", `{"stream":true}`, ""},
		{"stream string true", `{"stream":"true"}`, "--stream must be true or false"},
		{"stream string one", `{"stream":"1"}`, "--stream must be true or false"},
		{"stream numeric one", `{"stream":1}`, "--stream must be true or false"},
		{"stream numeric two", `{"stream":2}`, "--stream must be true or false"},
		{"stream object", `{"stream":{}}`, "--stream must be true or false"},
		{"stream array", `{"stream":[true]}`, "--stream must be true or false"},
		{"count minimum", `{"n":1}`, ""},
		{"count maximum", `{"n":10}`, ""},
		{"integer decimal", `{"n":2.0}`, ""},
		{"integer exponent", `{"n":1e1}`, ""},
		{"zero count", `{"n":0}`, "--count (-n) must be a whole number from 1 to 10"},
		{"negative count", `{"n":-1}`, "--count (-n) must be a whole number from 1 to 10"},
		{"too many", `{"n":11}`, "--count (-n) must be a whole number from 1 to 10"},
		{"fractional count", `{"n":1.5}`, "--count (-n) must be a whole number from 1 to 10"},
		{"huge count", `{"n":1e400}`, "--count (-n) must be a whole number from 1 to 10"},
		{"string count", `{"n":"2"}`, "--count (-n) must be a whole number from 1 to 10"},
		{"boolean count", `{"n":true}`, "--count (-n) must be a whole number from 1 to 10"},
		{"array count", `{"n":[1]}`, "--count (-n) must be a whole number from 1 to 10"},
		{"legacy count valid", `{"model":"dall-e-3","n":1}`, ""},
		{"legacy count null", `{"model":"dall-e-3","n":null}`, ""},
		{"legacy count too many", `{"model":"dall-e-3","n":2}`, "dall-e-3 supports exactly one image"},
		{"other legacy count", `{"model":"dall-e-2","n":10}`, ""},
		{"no partials", `{"partial_images":0}`, ""},
		{"streaming partials", `{"partial_images":3,"stream":true}`, ""},
		{"partial negative", `{"partial_images":-1,"stream":true}`, "--partial-images must be a whole number from 0 to 3"},
		{"partial too many", `{"partial_images":4,"stream":true}`, "--partial-images must be a whole number from 0 to 3"},
		{"partial fractional", `{"partial_images":1.5,"stream":true}`, "--partial-images must be a whole number from 0 to 3"},
		{"partial string", `{"partial_images":"1","stream":true}`, "--partial-images must be a whole number from 0 to 3"},
		{"partial mode selected later", `{"partial_images":1}`, ""},
		{"partial false streaming selected later", `{"partial_images":1,"stream":false}`, ""},
		{"partial null streaming selected later", `{"partial_images":1,"stream":null}`, ""},
		{"partial multiple images", `{"partial_images":1,"n":2}`, "streaming and partial images support exactly one image"},
		{"stream multiple images", `{"stream":true,"n":2}`, "streaming and partial images support exactly one image"},
		{"stream one image", `{"stream":true,"n":1}`, ""},
		{"transparent jpeg", `{"background":"transparent","output_format":"jpeg"}`, "JPEG does not support transparent backgrounds"},
		{"transparent png", `{"background":"transparent","output_format":"png"}`, ""},
		{"transparent webp", `{"background":"transparent","output_format":"webp"}`, ""},
		{"opaque jpeg", `{"background":"opaque","output_format":"jpeg"}`, ""},
		{"future options", `{"model":"future-image-model","quality":"future-quality","size":"future-size","output_format":"future-format","background":"future-background","n":2}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateImageGenerationSettings(gjson.Parse(test.body))
			if test.want == "" {
				if err != nil {
					t.Errorf("valid settings rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("validation = %v; want %q", err, test.want)
			}
		})
	}
}

func TestImageSettingsValidationDoesNotEchoInput(t *testing.T) {
	for _, body := range []string{
		`{"n":"private text\u001b]0;injected\u0007"}`,
		`{"stream":"private text\u001b]0;injected\u0007"}`,
	} {
		err := validateImageGenerationSettings(gjson.Parse(body))
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "injected") {
			t.Fatalf("invalid value was not handled safely: %v", err)
		}
	}
}
