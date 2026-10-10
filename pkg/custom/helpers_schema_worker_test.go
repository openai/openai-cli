package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	if handled, code := RunSchemaValidationHelper(os.Args, os.Stdin, os.Stdout); handled {
		os.Exit(code)
	}
}

func TestHelperSchemaCompilerProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"valid", `{"type":"object","properties":{"name":{"type":"string"}}}`, true},
		{"invalid", `{"type":"wrong"}`, false},
		{"remote", `{"$ref":"https://example.invalid/private"}`, false},
		{"file", `{"$ref":"file:///private/schema.json"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := runSchemaCompiler(ctx, executable, []byte(tc.data))
			if (err == nil) != tc.valid {
				t.Fatalf("validation result: %v", err)
			}
		})
	}
}

func TestHelperSchemaCompilerCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runSchemaCompiler(ctx, executable, []byte(`{"type":"object"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestHelperSchemaCompilerDispatch(t *testing.T) {
	for _, args := range [][]string{{"openai"}, {"openai", "--help"}, {"openai", schemaValidationArgument, "extra"}} {
		if handled, _ := RunSchemaValidationHelper(args, strings.NewReader("{}"), io.Discard); handled {
			t.Fatal("intercepted ordinary invocation")
		}
	}
}
