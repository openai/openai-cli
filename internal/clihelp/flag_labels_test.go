package clihelp

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

type wrappedStringHelpFlag struct{ *cli.StringFlag }

func (f *wrappedStringHelpFlag) CLIStringFlag() *cli.StringFlag { return f.StringFlag }

func TestFlagValueLabelsDescribeAcceptedInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag cli.Flag
		want string
	}{
		{"integer", &requestflag.Flag[int64]{Name: "max-items"}, "INTEGER"},
		{"nullable integer", &requestflag.Flag[*int64]{Name: "n"}, "INTEGER"},
		{"number", &requestflag.Flag[*float64]{Name: "temperature"}, "NUMBER"},
		{"nullable boolean", &requestflag.Flag[*bool]{Name: "stream"}, "BOOLEAN"},
		{"boolean switch", &cli.BoolFlag{Name: "debug"}, ""},
		{"request boolean switch", &requestflag.Flag[bool]{Name: "active"}, ""},
		{"model", &requestflag.Flag[string]{Name: "model"}, "MODEL"},
		{"format", &cli.StringFlag{Name: "format"}, "FORMAT"},
		{"JSON path", &cli.StringFlag{Name: "transform"}, "PATH"},
		{"directory", &cli.StringFlag{Name: "output-dir"}, "DIRECTORY"},
		{"URL", &cli.StringFlag{Name: "base-url"}, "URL"},
		{"prompt", &requestflag.Flag[string]{Name: "prompt"}, "TEXT"},
		{"file path", &requestflag.Flag[any]{Name: "image", FileInput: true}, "PATH"},
		{"string file path", &cli.StringFlag{Name: "certificate", TakesFile: true}, "PATH"},
		{"wrapped file path", &wrappedStringHelpFlag{&cli.StringFlag{Name: "certificate", TakesFile: true}}, "PATH"},
		{"wrapped ordinary string", &wrappedStringHelpFlag{&cli.StringFlag{Name: "certificate"}}, "TEXT"},
		{"ordinary string", &cli.StringFlag{Name: "file"}, "TEXT"},
		{"string map", &cli.StringMapFlag{Name: "field"}, "TEXT=TEXT"},
		{"polymorphic input", &requestflag.Flag[any]{Name: "input"}, "VALUE"},
		{"polymorphic model", &requestflag.Flag[any]{Name: "model"}, "VALUE"},
		{"numeric format", &cli.IntFlag{Name: "format"}, "INTEGER"},
		{"credential", &cli.StringFlag{Name: "api-key", Value: "fake-private-test-key", HideDefault: true}, "TEXT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := flagValueLabel(tc.flag); got != tc.want {
				t.Fatalf("label = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestFileValueLabelsPreserveHiddenDefaults(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		value := &cli.StringFlag{Name: "certificate", TakesFile: true, Value: "synthetic-private-path", HideDefault: true}
		var flag cli.Flag = value
		if wrapped {
			flag = &wrappedStringHelpFlag{value}
		}
		before := flag.String()
		if got := fullFlag(flag); !strings.Contains(got, "--certificate PATH") || strings.Contains(got, value.Value) {
			t.Fatalf("file help lost its label or exposed its value: %q", got)
		}
		if flag.String() != before || !value.HideDefault || value.Value != "synthetic-private-path" {
			t.Fatal("file label changed flag metadata or values")
		}
	}
}

func TestFlagTypeLabelsPreserveMapsAndDates(t *testing.T) {
	for name, want := range map[string]string{
		"date": "DATE", "datetime": "DATETIME", "time": "TIME",
		"string=string": "TEXT=TEXT", "string=int": "TEXT=INTEGER", "string=any": "TEXT=VALUE",
	} {
		if got := flagTypeLabel(name); got != want {
			t.Errorf("type %q label = %q; want %q", name, got, want)
		}
	}
}
