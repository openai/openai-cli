package requestflag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestInputPolicyInnerArrayOperand(t *testing.T) {
	outer := &requestflag.Flag[map[string]any]{Name: "notification-channel", BodyPath: "notification_channel", InputPolicy: requestflag.InputPolicyTyped}
	inner := &requestflag.InnerFlag[[]string]{Name: "notification-channel.recipients", OuterFlag: outer, InnerField: "recipients", InputPolicy: requestflag.InputPolicyTyped}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{outer, inner}, Writer: io.Discard, ErrWriter: io.Discard}
	err := cmd.Run(context.Background(), []string{"test", "--notification-channel.recipients", `["synthetic@example.test"]`})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInputPolicyOverwrittenStructuredScalarSource(t *testing.T) {
	flag := &requestflag.Flag[*string]{Name: "instructions", BodyPath: "instructions", InputPolicy: requestflag.InputPolicyTextStructured}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{flag}, Writer: io.Discard, ErrWriter: io.Discard}
	requestflag.AddStructuredFlags(cmd)
	if err := cmd.Run(context.Background(), []string{"test", "--instructions-structured", "@synthetic-missing.json", "--instructions-structured", `"replacement"`}); err != nil {
		t.Fatal(err)
	}
	got, err := requestconfig.EmbedFiles(flag.Get(), requestconfig.EmbedText, nil)
	if err != nil {
		t.Fatalf("overwritten source was read: %v", err)
	}
	if got != "replacement" {
		t.Fatalf("got %#v", got)
	}
}

func TestInputPolicyStructuredParentOrdinaryNestedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.txt")
	if err := os.WriteFile(path, []byte("synthetic file content"), 0600); err != nil {
		t.Fatal(err)
	}
	outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entry", InputPolicy: requestflag.InputPolicyTextStructured}
	inner := &requestflag.InnerFlag[string]{Name: "entry.content", OuterFlag: outer, InnerField: "content", InputPolicy: requestflag.InputPolicyText}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{outer, inner}, Writer: io.Discard, ErrWriter: io.Discard}
	requestflag.AddStructuredFlags(cmd)
	if err := cmd.Run(context.Background(), []string{"test", "--entry-structured", `{}`, "--entry.content", "@" + path}); err != nil {
		t.Fatal(err)
	}
	got, err := requestconfig.EmbedFiles(outer.Get(), requestconfig.EmbedText, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["content"] != "synthetic file content" {
		t.Fatalf("ordinary nested source stayed literal: %#v", got)
	}
}

func TestInputPolicyOverwrittenStructuredScalarStdin(t *testing.T) {
	flag := &requestflag.Flag[*string]{Name: "instructions", BodyPath: "instructions", InputPolicy: requestflag.InputPolicyTextStructured}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{flag}, Writer: io.Discard, ErrWriter: io.Discard}
	requestflag.AddStructuredFlags(cmd)
	if err := cmd.Run(context.Background(), []string{"test", "--instructions-structured", "@-", "--instructions-structured", `"replacement"`}); err != nil {
		t.Fatal(err)
	}
	contents := requestflag.ExtractRequestContents(cmd)
	consumers, err := requestconfig.StdinConsumers(contents, false)
	if err != nil {
		t.Fatal(err)
	}
	if consumers != 0 {
		t.Fatalf("overwritten source still claims stdin: %d", consumers)
	}
	_, err = requestconfig.EmbedFiles(flag.Get(), requestconfig.EmbedText, &requestconfig.OnceStdinReader{Reader: strings.NewReader("not structured [")})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInputPolicyTypedParentAfterNested(t *testing.T) {
	for _, mode := range []string{"literal", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entries", InputPolicy: requestflag.InputPolicyTyped}
			inner := &requestflag.InnerFlag[string]{Name: "entry.type", OuterFlag: outer, InnerField: "type", OuterIsArrayOfObjects: true, InputPolicy: requestflag.InputPolicyText}
			cmd := &cli.Command{Name: "test", Flags: []cli.Flag{outer, inner}, Writer: io.Discard, ErrWriter: io.Discard}
			if err := requestflag.ConfigureInputMode(cmd, mode); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Run(context.Background(), []string{"test", "--entry.type", "first", "--entry", `{"type":"second"}`}); err != nil {
				t.Fatal(err)
			}
			got := outer.Get().([]map[string]any)
			if len(got) != 2 {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestInputPolicyUntrustedPipedInnerUnderStructuredParent(t *testing.T) {
	t.Setenv(requestconfig.UntrustedStdinEnv, "true")
	path := filepath.Join(t.TempDir(), "private-synthetic.txt")
	if err := os.WriteFile(path, []byte("SYNTHETIC FILE CANARY"), 0600); err != nil {
		t.Fatal(err)
	}
	outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entry", InputPolicy: requestflag.InputPolicyTextStructured}
	inner := &requestflag.InnerFlag[string]{Name: "entry.content", OuterFlag: outer, InnerField: "content", InputPolicy: requestflag.InputPolicyText}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{outer, inner}, Writer: io.Discard, ErrWriter: io.Discard}
	requestflag.AddStructuredFlags(cmd)
	if err := cmd.Run(context.Background(), []string{"test", "--entry-structured", `{"type":"synthetic"}`}); err != nil {
		t.Fatal(err)
	}
	contents := requestflag.ExtractRequestContents(cmd)
	security, err := requestconfig.NewStdinSecurity()
	if err != nil {
		t.Fatal(err)
	}
	piped, err := json.Marshal(map[string]map[string]string{"entry": {"content": "@" + path}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestconfig.ReadPipedInput(cmd, bytes.NewReader(piped), &contents, security, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := requestconfig.EmbedFiles(contents.Body, requestconfig.EmbedText, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := got.(map[string]any)["entry"].(map[string]any)["content"]
	if content != "@"+path {
		t.Fatalf("untrusted piped string expanded: %#v", content)
	}
}
