package requestflag

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpDefaultDoesNotDependOnParsing(t *testing.T) {
	for _, tc := range []struct {
		name, want, override string
		flag                 cli.Flag
	}{
		{"string", "auto", "high", &Flag[string]{Name: "quality", Default: "auto"}},
		{"integer", "1", "2", &Flag[int64]{Name: "count", Default: 1}},
		{"zero", "0", "3", &Flag[int64]{Name: "partials"}},
		{"boolean", "false", "true", &Flag[bool]{Name: "stream"}},
		{"nullable", "null", "model-id", &Flag[*string]{Name: "model"}},
		{"pointer", "png", "jpeg", &Flag[*string]{Name: "output-format", Default: Ptr("png")}},
		{"explicit description", "chosen automatically", "high", &Flag[string]{Name: "quality", Default: "auto", DefaultText: "chosen automatically"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.flag.(cli.DocGenerationFlag)
			if got := doc.GetDefaultText(); got != tc.want {
				t.Fatalf("before parsing: default = %q; want %q", got, tc.want)
			}
			if err := tc.flag.PreParse(); err != nil {
				t.Fatal(err)
			}
			if err := tc.flag.Set(tc.flag.Names()[0], tc.override); err != nil {
				t.Fatal(err)
			}
			if got := doc.GetDefaultText(); got != tc.want {
				t.Fatalf("after setting value: default = %q; want %q", got, tc.want)
			}
			if got := doc.GetValue(); got != tc.override {
				t.Fatalf("help changed request value: got %q; want %q", got, tc.override)
			}
		})
	}
}

func TestHelpDefaultDoesNotRunValidationOrRevealHiddenValue(t *testing.T) {
	flag := &Flag[string]{Name: "example", Default: "auto", Validator: func(string) error {
		t.Fatal("showing a default must not run validation")
		return nil
	}}
	if !strings.Contains(flag.String(), "(default: auto)") || flag.IsSet() {
		t.Fatalf("default not displayed without parsing: %s", flag.String())
	}
	flag.HideDefault = true
	flag.Default = "fake-hidden-default"
	if strings.Contains(flag.String(), "fake-hidden-default") {
		t.Fatal("hidden default was displayed")
	}
}
