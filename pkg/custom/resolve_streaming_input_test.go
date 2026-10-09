package custom

import (
	"bytes"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestResolveStreamingInputMatrix(t *testing.T) {
	for _, test := range []struct {
		name, body, explicit string
		wantSet              bool
		want                 *bool
	}{
		{"true", `{"stream":true,"input":"synthetic"}`, "", true, requestflag.Ptr(true)},
		{"false", `{"stream":false}`, "", true, requestflag.Ptr(false)},
		{"missing", `{"input":"synthetic"}`, "", false, nil},
		{"null", `{"stream":null}`, "", false, nil},
		{"quoted_true", `{"stream":"true"}`, "", false, nil},
		{"number", `{"stream":1}`, "", false, nil},
		{"array", `{"stream":[true]}`, "", false, nil},
		{"nested", `{"nested":{"stream":true}}`, "", false, nil},
		{"explicit_false", `{"stream":true}`, "false", true, requestflag.Ptr(false)},
		{"explicit_null", `{"stream":true}`, "null", true, nil},
		{"explicit_true", `{"stream":false}`, "true", true, requestflag.Ptr(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			flag := &requestflag.Flag[*bool]{Name: "stream", BodyPath: "stream"}
			if test.explicit != "" {
				if err := flag.Set("stream", test.explicit); err != nil {
					t.Fatal(err)
				}
			}
			cmd := &cli.Command{Flags: []cli.Flag{flag}}
			original := []byte(test.body)
			body := bytes.Clone(original)
			if err := resolveStreamingInput(cmd, body); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(body, original) {
				t.Fatal("body changed")
			}
			if flag.IsSet() != test.wantSet {
				t.Fatalf("set=%v want %v", flag.IsSet(), test.wantSet)
			}
			got := flag.Get().(*bool)
			if (got == nil) != (test.want == nil) || got != nil && *got != *test.want {
				t.Fatalf("value=%v want %v", got, test.want)
			}
		})
	}
	for _, flag := range []cli.Flag{
		&requestflag.Flag[bool]{Name: "stream", BodyPath: "unrelated"},
		&requestflag.Flag[bool]{Name: "unrelated", BodyPath: "stream"},
		&requestflag.Flag[string]{Name: "stream", BodyPath: "stream"},
		&cli.BoolFlag{Name: "stream"},
	} {
		t.Run("unrelated_"+flag.Names()[0], func(t *testing.T) {
			if err := resolveStreamingInput(&cli.Command{Flags: []cli.Flag{flag}}, []byte(`{"stream":true}`)); err != nil {
				t.Fatal(err)
			}
			if flag.IsSet() {
				t.Fatal("unrelated flag changed")
			}
		})
	}
	flag := &requestflag.Flag[bool]{Name: "stream", BodyPath: "stream"}
	if err := resolveStreamingInput(&cli.Command{Flags: []cli.Flag{flag}}, []byte(`{"stream":true}`)); err != nil {
		t.Fatal(err)
	}
	if !flag.Get().(bool) {
		t.Fatal("bool flag was not set")
	}
}
