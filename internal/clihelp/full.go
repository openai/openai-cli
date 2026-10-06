package clihelp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// FlagGroup is presentation metadata supplied by handwritten features. It never
// changes parser order, categories, flag aliases or request defaults.
type FlagGroup struct {
	Title string
	Names []string
	// Owner restricts a group to flags declared by this command. Nil matches by name.
	Owner *cli.Command
}

// Keep the framework's command reference and its complete flag descriptions.
// Only the option layout changes, using headings rather than a wide flag table.
func fullHelpTemplate(command *cli.Command, source string) string {
	configureCommandList(command)
	command.Metadata["full-help-flags"] = func(flags []cli.Flag) string {
		return fullFlagGroups(command, flags, helpWidth(command))
	}
	command.Metadata["full-help-description"] = func(text string) string {
		return strings.TrimSuffix(wrapDescription(text, "   ", helpWidth(command)), "\n")
	}
	return strings.NewReplacer(
		`COMMANDS:{{template "visibleCommandCategoryTemplate" .}}`, `{{call (index .Metadata "help-command-list")}}`,
		`COMMANDS:{{template "visibleCommandTemplate" .}}`, `{{call (index .Metadata "help-command-list")}}`,
		`{{template "visibleFlagCategoryTemplate" .}}`, `{{call (index .Metadata "full-help-flags") .VisibleFlags}}`,
		`{{template "visibleFlagTemplate" .}}`, `{{call (index .Metadata "full-help-flags") .VisibleFlags}}`,
		`{{template "visiblePersistentFlagTemplate" .}}`, `{{call (index .Metadata "full-help-flags") .VisiblePersistentFlags}}`,
		`   {{template "descriptionTemplate" .}}`, `{{call (index .Metadata "full-help-description") .Description}}`,
	).Replace(source)
}

func fullFlagGroups(command *cli.Command, flags []cli.Flag, width int) string {
	groups := []FlagGroup{{Title: "Required inputs"}}
	if configured, ok := command.Metadata["help-flag-groups"].([]FlagGroup); ok {
		groups = append(groups, configured...)
	}
	groups = append(groups, FlagGroup{Title: "Other options"})
	grouped := make(map[string][]cli.Flag)
	for _, flag := range flags {
		group := "Other options"
		if isRequired(flag) {
			group = "Required inputs"
		} else if category, ok := flag.(cli.CategorizableFlag); ok && category.GetCategory() != "" {
			group = category.GetCategory()
		} else {
			for _, section := range groups {
				if section.Owner != nil && !slices.Contains(section.Owner.Flags, flag) {
					continue
				}
				for _, name := range section.Names {
					for _, alias := range flag.Names() {
						if alias == name {
							group = section.Title
						}
					}
				}
			}
		}
		if _, exists := grouped[group]; !exists {
			groups = append(groups, FlagGroup{Title: group})
		}
		grouped[group] = append(grouped[group], flag)
	}
	var out strings.Builder
	for _, group := range groups {
		flags := grouped[group.Title]
		if len(flags) == 0 {
			continue
		}
		fmt.Fprintf(&out, "\n\n   %s\n", group.Title)
		for _, flag := range flags {
			heading, description, _ := strings.Cut(fullFlag(flag), "\t")
			fmt.Fprintf(&out, "\n   %s\n", heading)
			out.WriteString(strings.TrimSuffix(wrapDescription(description, "      ", width), "\n"))
		}
		delete(grouped, group.Title)
	}
	return out.String()
}

func fullFlag(flag cli.Flag) string {
	rendered := fileInputUsage(flag, flag.String())
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
	// Keep descriptions, defaults, and environment hints after file-input wording.
	return fullFlagNames(flag, label) + "\t" + details
}

func fileInputUsage(flag cli.Flag, text string) string {
	if input, ok := flag.(interface{ IsFileInput() bool }); ok && input.IsFileInput() {
		// These SDK descriptions describe objects, but FileInput accepts local
		// paths. Keep every format, size constraint, and environment hint.
		text = strings.NewReplacer(
			"The audio file object (not file name) to transcribe", "Path to the audio file to transcribe",
			"The audio file object (not file name) translate", "Path to the audio file to translate",
			"The File object (not file name) to be uploaded", "Path to the file to upload",
		).Replace(text)
	}
	return text
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
