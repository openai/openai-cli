package cmd

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpImageReferencePreservesGeneratedDescriptions(t *testing.T) {
	// Compare with the generator-owned source rather than a copied fixture, so
	// added parameters and updated model limits are covered automatically.
	source, err := parser.ParseFile(token.NewFileSet(), "image.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var generated *ast.CompositeLit
	for _, declaration := range source.Decls {
		declaration, ok := declaration.(*ast.GenDecl)
		if !ok || declaration.Tok != token.VAR {
			continue
		}
		for _, specification := range declaration.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "imagesGenerate" {
				continue
			}
			if len(value.Values) != 1 {
				t.Fatal("generated imagesGenerate must have one initializer")
			}
			generated, ok = value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatal("generated imagesGenerate initializer is not a command literal")
			}
		}
	}
	if generated == nil {
		t.Fatal("generated imagesGenerate command not found")
	}
	flags, ok := imageHelpSourceField(t, generated, "Flags").(*ast.CompositeLit)
	if !ok || len(flags.Elts) == 0 {
		t.Fatal("generated imagesGenerate Flags must be a nonempty literal")
	}
	visible := make(map[string]cli.Flag)
	for _, flag := range imagesGenerate.VisibleFlags() {
		for _, name := range flag.Names() {
			visible[name] = flag
		}
	}
	for _, expression := range flags.Elts {
		pointer, ok := expression.(*ast.UnaryExpr)
		if !ok || pointer.Op != token.AND {
			t.Fatalf("generated flag is not an address expression: %T", expression)
		}
		flag, ok := pointer.X.(*ast.CompositeLit)
		if !ok {
			t.Fatalf("generated flag is not a literal: %T", pointer.X)
		}
		name := imageHelpSourceString(t, imageHelpSourceField(t, flag, "Name"))
		usage := imageHelpSourceString(t, imageHelpSourceField(t, flag, "Usage"))
		t.Run(name, func(t *testing.T) {
			current, ok := visible[name]
			if !ok {
				t.Fatalf("generated flag %q is missing from visible image help", name)
			}
			documentation, ok := current.(cli.DocGenerationFlag)
			if !ok {
				t.Fatalf("flag %q does not expose its documentation", name)
			}
			if usage == "" || !strings.Contains(documentation.GetUsage(), usage) {
				t.Errorf("flag %q lost its generated API description\ngenerated: %s\ncurrent: %s", name, usage, documentation.GetUsage())
			}
			// Display may remove Markdown backticks and wrap paragraphs, but it
			// must not drop any of the generated explanation or constraints.
			normalize := func(text string) string {
				return strings.Join(strings.Fields(strings.ReplaceAll(text, "`", "")), " ")
			}
			if rendered := renderImageReferenceFlag(current); !strings.Contains(normalize(rendered), normalize(usage)) {
				t.Errorf("rendered help for %q lost API documentation: %s", name, rendered)
			}
		})
	}
}

func imageHelpSourceField(t *testing.T, literal *ast.CompositeLit, name string) ast.Expr {
	t.Helper()
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == name {
			return field.Value
		}
	}
	t.Fatalf("generated literal has no %s field", name)
	return nil
}

func imageHelpSourceString(t *testing.T, expression ast.Expr) string {
	t.Helper()
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Fatalf("generated help must use a string literal: %T", expression)
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHelpImageUsageErrorPreservesOriginalError(t *testing.T) {
	var output bytes.Buffer
	command := &cli.Command{Name: "openai", ErrWriter: &output, Metadata: map[string]any{"help-invocation": "./openai"}}
	original := cli.Exit("unexpected\n\x1b[2Joption", 17)
	got := imageGenerateUsageError(context.Background(), command, original, false)
	if got != original || got.(cli.ExitCoder).ExitCode() != 17 {
		t.Fatalf("usage handler changed original error or exit code: %v", got)
	}
	if strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), `\x1b`) || !strings.Contains(output.String(), `unexpected\n`) {
		t.Fatalf("unexpected parser details were not safely quoted: %q", output.String())
	}
	if !strings.Contains(output.String(), "./openai images generate --help") {
		t.Fatalf("usage handler lost local invocation: %q", output.String())
	}
}
