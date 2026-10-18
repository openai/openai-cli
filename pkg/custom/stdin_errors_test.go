package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestStdinValueErrorUsesEffectiveCommandScope(t *testing.T) {
	for _, scope := range []string{"selected child", "shadowed alias", "inherited alias", "hidden", "nonpersistent ancestor", "unknown declaration"} {
		t.Run(scope, func(t *testing.T) {
			flag := &requestflag.Flag[int64]{Name: "limit", QueryPath: "limit"}
			leaf := &cli.Command{Name: "create", Flags: []cli.Flag{flag}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "limit"}}, Commands: []*cli.Command{{Name: "group", Commands: []*cli.Command{leaf}}}}
			source := leaf
			want := "Invalid piped input for --limit. Expected an integer, such as 1."
			switch scope {
			case "shadowed alias":
				flag.Name, flag.Aliases = "count", []string{"p"}
				root.Flags = []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}}
				want = "Invalid piped input for --count. Expected an integer, such as 1."
			case "inherited alias":
				flag.Name = "n"
				root.Flags = []cli.Flag{&cli.IntFlag{Name: "count", Aliases: []string{"n"}}}
				leaf.Flags = nil
				source = &cli.Command{Flags: []cli.Flag{flag}}
				want = "Invalid piped input for --count. Expected an integer, such as 1."
			case "hidden":
				flag.Name, flag.Hidden = "synthetic-private-hidden", true
				root.Flags = nil
				want = "Could not apply piped input. Check the input's field names and value types."
			case "nonpersistent ancestor":
				flag.Name = "synthetic-private-ancestor"
				root.Flags, leaf.Flags = []cli.Flag{flag}, nil
				source = root
				want = "Could not apply piped input. Check the input's field names and value types."
			case "unknown declaration":
				flag.Name = "synthetic-private-unknown"
				root.Flags, leaf.Flags = nil, nil
				source = &cli.Command{Flags: []cli.Flag{flag}}
				want = "Could not apply piped input. Check the input's field names and value types."
			}
			leaf.Action = func(context.Context, *cli.Command) error {
				return requestflag.ApplyStdinDataToFlags(source, map[string]any{"limit": "synthetic-private-value"})
			}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "group", "create"})
			var typed *requestflag.StdinValueError
			if !errors.As(err, &typed) {
				t.Fatalf("expected typed stdin failure, got %v", err)
			}
			want += "\nOptions and examples: openai group create --help"
			if got := localErrorMessage(root, err); got != want || strings.Contains(got, "synthetic-private") {
				t.Fatalf("wrong scoped stdin recovery: got %q, want %q", got, want)
			}
		})
	}
}

func TestStdinValueErrorOutranksGenericJSONCause(t *testing.T) {
	var value any
	syntax := json.Unmarshal([]byte(`{"synthetic-private":`), &value)
	typeFailure := &json.UnmarshalTypeError{Value: "synthetic-private-value", Type: reflect.TypeFor[int64](), Field: "synthetic-private-field"}
	for _, cause := range []error{syntax, typeFailure} {
		t.Run(reflect.TypeOf(cause).String(), func(t *testing.T) {
			flag := &requestflag.Flag[int64]{Name: "limit", QueryPath: "limit", Validator: func(value int64) error {
				if value == 1 {
					return cause
				}
				return nil
			}}
			leaf := &cli.Command{Name: "list", Flags: []cli.Flag{flag}}
			leaf.Action = func(context.Context, *cli.Command) error {
				return requestflag.ApplyStdinDataToFlags(leaf, map[string]any{"limit": 1})
			}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{leaf}}
			ConfigureCommandErrors(root)
			err := root.Run(t.Context(), []string{"openai", "list"})
			if !errors.Is(err, cause) {
				t.Fatalf("lost JSON cause: %v", err)
			}
			want := "Invalid piped input for --limit. Expected an integer, such as 1.\nOptions and examples: openai list --help"
			if got := localErrorMessage(root, err); got != want {
				t.Fatalf("generic JSON cause masked stdin provenance: %q", got)
			}
			for _, cancel := range []struct {
				cause error
				want  string
			}{
				{context.Canceled, "Request canceled."},
				{context.DeadlineExceeded, "The request timed out. The API may have received it; check its status before repeating it."},
			} {
				if got := localErrorMessage(root, errors.Join(err, cancel.cause)); got != cancel.want {
					t.Fatalf("stdin guidance masked cancellation: %q", got)
				}
			}
		})
	}
}
