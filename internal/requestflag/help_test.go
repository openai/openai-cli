package requestflag

import (
	"encoding/json"
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
		{"nullable string", "", "model-id", &Flag[*string]{Name: "model"}},
		{"nullable integer", "", "2", &Flag[*int64]{Name: "count"}},
		{"nullable float", "", "0.5", &Flag[*float64]{Name: "temperature"}},
		{"nullable boolean", "", "true", &Flag[*bool]{Name: "enabled"}},
		{"nullable date", "", "2026-01-01", &Flag[*DateValue]{Name: "date"}},
		{"nullable time", "", "12:00:00", &Flag[*TimeValue]{Name: "time"}},
		{"nullable datetime", "", "2026-01-01T12:00:00Z", &Flag[*DateTimeValue]{Name: "datetime"}},
		{"constant null", "null", "model-id", &Flag[*string]{Name: "model", Const: true}},
		{"nullable explicit description", "unset", "model-id", &Flag[*string]{Name: "model", DefaultText: "unset"}},
		{"pointer", "png", "jpeg", &Flag[*string]{Name: "output-format", Default: Ptr("png")}},
		{"pointer false", "false", "true", &Flag[*bool]{Name: "enabled", Default: Ptr(false)}},
		{"pointer zero", "0", "2", &Flag[*int64]{Name: "count", Default: Ptr(int64(0))}},
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

func TestHelpPreservesNullableRequestSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		input      *string
	}{
		{"omitted", `{}`, nil},
		{"explicit null", `{"description":null}`, Ptr("null")},
		{"value", `{"description":"example"}`, Ptr("example")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := &Flag[*string]{Name: "description", BodyPath: "description"}
			if err := flag.PreParse(); err != nil {
				t.Fatal(err)
			}
			if tc.input != nil {
				if err := flag.Set("description", *tc.input); err != nil {
					t.Fatal(err)
				}
			}
			if got := flag.GetDefaultText(); got != "" {
				t.Fatalf("unset default described as %q", got)
			}
			if strings.Contains(flag.String(), "(default:") {
				t.Fatalf("help claims a default: %s", flag.String())
			}
			if got, want := flag.IsSet(), tc.input != nil; got != want {
				t.Fatalf("help changed IsSet: got %v; want %v", got, want)
			}
			command := &cli.Command{Flags: []cli.Flag{flag}}
			body, err := json.Marshal(ExtractRequestContents(command).Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != tc.want {
				t.Fatalf("request body = %s; want %s", body, tc.want)
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
