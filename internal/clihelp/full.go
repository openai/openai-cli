package clihelp

import (
	"strings"

	"github.com/urfave/cli/v3"
)

// Keep the framework's full reference layout and metadata. Only flag headings
// need a different renderer: API prose uses backticks for examples, whereas
// urfave treats the first backticked phrase as an argument placeholder.
func fullHelpTemplate(command *cli.Command, source string) string {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["full-help-flag"] = fullFlag
	return strings.NewReplacer(
		`{{template "visibleFlagCategoryTemplate" .}}`, `{{range .VisibleFlagCategories}}
   {{if .Name}}{{.Name}}

   {{end}}{{$flglen := len .Flags}}{{range $i, $e := .Flags}}{{if eq (subtract $flglen $i) 1}}{{call (index $.Metadata "full-help-flag") $e}}
{{else}}{{call (index $.Metadata "full-help-flag") $e}}
   {{end}}{{end}}{{end}}`,
		`{{template "visibleFlagTemplate" .}}`, `{{range $i, $e := .VisibleFlags}}
   {{wrap (call (index $.Metadata "full-help-flag") $e) 6}}{{end}}`,
		`{{template "visiblePersistentFlagTemplate" .}}`, `{{range $i, $e := .VisiblePersistentFlags}}
   {{wrap (call (index $.Metadata "full-help-flag") $e) 6}}{{end}}`,
	).Replace(source)
}

func fullFlag(flag cli.Flag) string {
	rendered := flag.String()
	doc, ok := flag.(cli.DocGenerationFlag)
	if !ok {
		return rendered
	}
	_, quoted, found := strings.Cut(doc.GetUsage(), "`")
	placeholder, _, closed := strings.Cut(quoted, "`")
	if !found || !closed || placeholder == "" {
		return rendered
	}
	heading, details, separated := strings.Cut(rendered, "\t")
	if !separated || heading != fullFlagNames(flag, placeholder) {
		// Preserve flags with their own presentation, such as --[no-]color.
		return rendered
	}
	label := ""
	if doc.TakesValue() {
		label = doc.TypeName()
		if label == "" {
			label = "value"
		}
	}
	// Keep the original description, defaults, and environment hints verbatim.
	return fullFlagNames(flag, label) + "\t" + details
}

func fullFlagNames(flag cli.Flag, placeholder string) string {
	var names []string
	for _, name := range flag.Names() {
		if name == "" {
			continue
		}
		prefix := "--"
		if len(name) == 1 {
			prefix = "-"
		}
		if placeholder != "" {
			name += " " + placeholder
		}
		names = append(names, prefix+name)
	}
	heading := strings.Join(names, ", ")
	if multi, ok := flag.(cli.DocGenerationMultiValueFlag); ok && multi.IsMultiValueFlag() {
		heading += " [ " + heading + " ]"
	}
	return heading
}
