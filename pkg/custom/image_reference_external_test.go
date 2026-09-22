package custom_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"strconv"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func TestHelpImageReferencePreservesGeneratedDescriptions(t *testing.T) {
	// Compare with the generator-owned source rather than a copied fixture, so
	// added parameters and updated model limits are covered automatically.
	source, err := parser.ParseFile(token.NewFileSet(), "../cmd/image.go", nil, 0)
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

	// Exercise the public help path with the real generated command tree and
	// its registered customizations. Copies isolate help's command metadata
	// and parent links; the flag definitions themselves remain the real ones.
	root := copyImageHelpCommandTree(cmd.Command)
	images := root.Command("images")
	if images == nil {
		t.Fatal("images command is missing from the generated command tree")
	}
	generate := images.Command("generate")
	if generate == nil {
		t.Fatal("images generate command is missing from the generated command tree")
	}
	var output bytes.Buffer
	root.Writer, root.ErrWriter = &output, &output
	root.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) {
		t.Fatal("help ran request setup")
		return ctx, nil
	}
	generate.Action = func(context.Context, *cli.Command) error {
		t.Fatal("help ran the image API action")
		return nil
	}
	args, help, err := custom.ConfigureHelp(root, []string{"openai", "help", "--all", "images", "generate"})
	if err != nil || !help {
		t.Fatalf("ConfigureHelp = %q, %v, %v", args, help, err)
	}
	if err := root.Run(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	for _, heading := range []string{"Image generation: full reference", "IMAGE OPTIONS", "GLOBAL OPTIONS"} {
		if !strings.Contains(rendered, heading) {
			t.Fatalf("full image help is missing %q: %s", heading, rendered)
		}
	}

	visible := make(map[string]cli.Flag)
	for _, flag := range generate.VisibleFlags() {
		for _, name := range flag.Names() {
			visible[name] = flag
		}
	}
	// Display may remove Markdown backticks and wrap paragraphs, but it must
	// not drop any of the generated explanation or constraints.
	normalize := func(text string) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(text, "`", "")), " ")
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
			if !strings.Contains(normalize(rendered), normalize(usage)) {
				t.Errorf("full rendered help lost API documentation for %q\ngenerated: %s\nrendered: %s", name, usage, rendered)
			}
		})
	}
}

func copyImageHelpCommandTree(command *cli.Command) *cli.Command {
	copied := *command
	copied.Metadata = maps.Clone(command.Metadata)
	copied.Flags = append([]cli.Flag(nil), command.Flags...)
	copied.Commands = make([]*cli.Command, len(command.Commands))
	for i, child := range command.Commands {
		copied.Commands[i] = copyImageHelpCommandTree(child)
	}
	return &copied
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
