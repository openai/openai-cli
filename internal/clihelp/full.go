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

// FlagUsage supplies display-only guidance for flags declared by Owner.
type FlagUsage struct {
	Owner *cli.Command
	Names []string
	Usage string
}

func commandFlagUsage(command *cli.Command, flag cli.Flag, usage string) string {
	configured, _ := command.Metadata["help-flag-usages"].([]FlagUsage)
	for _, entry := range configured {
		if entry.Owner == nil || !slices.Contains(entry.Owner.Flags, flag) {
			continue
		}
		for _, name := range entry.Names {
			if slices.Contains(flag.Names(), name) {
				return entry.Usage
			}
		}
	}
	return usage
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
			heading, description := flagReference(command, flag)
			fmt.Fprintf(&out, "\n   %s\n", heading)
			writeFlagDescription(&out, description, width)
		}
		delete(grouped, group.Title)
	}
	return out.String()
}

// Use documentation interfaces for defaults and environment names. Preserve a
// custom flag's own presentation when its signature differs from standard flags.
func flagReference(command *cli.Command, flag cli.Flag) (string, string) {
	heading, fallback, _ := strings.Cut(fullFlag(flag), "\t")
	doc, ok := flag.(cli.DocGenerationFlag)
	if !ok || heading != fullFlagNames(flag, flagValueLabel(flag)) {
		return heading, fallback
	}
	var names []string
	for _, name := range flag.Names() {
		prefix := "--"
		if len(name) == 1 {
			prefix = "-"
		}
		names = append(names, prefix+name)
	}
	heading = strings.Join(names, ", ")
	if label := commandFlagValueLabel(command, flag); label != "" {
		heading += " " + label
	}
	usage := fileInputUsage(flag, commandFlagUsage(command, flag, doc.GetUsage()))
	if multi, ok := flag.(cli.DocGenerationMultiValueFlag); ok && multi.IsMultiValueFlag() &&
		!strings.Contains(strings.ToLower(usage), "repeat") {
		usage += "\nCan be used more than once."
	}
	required, _ := flag.(cli.RequiredFlag)
	if doc.IsDefaultVisible() && (required == nil || !required.IsRequired()) {
		value := doc.GetDefaultText()
		// Request flags expose parsed values through GetValue. Their declared
		// defaults come only from GetDefaultText, including unset nullable values.
		_, request := flag.(interface{ IsRequiredAsFlagOrStdin() bool })
		if value == "" && !request {
			value = doc.GetValue()
		}
		if value != "" {
			usage += "\nDefault: " + value
		}
	}
	if env := doc.GetEnvVars(); len(env) > 0 {
		usage += "\nEnv: " + strings.Join(env, ", ")
	}
	return heading, strings.TrimSpace(usage)
}

func writeFlagDescription(out *strings.Builder, description string, width int) {
	var prose []string
	flush := func() {
		if len(prose) > 0 {
			out.WriteString(wrapDescription(strings.Join(prose, "\n"), "      ", width))
			prose = nil
		}
	}
	for _, line := range strings.Split(description, "\n") {
		// These are authored or interface-derived presentation lines. Preserve
		// their boundaries without inferring values from arbitrary flag prose.
		if strings.HasPrefix(line, "Default:") || strings.HasPrefix(line, "Env:") {
			flush()
			out.WriteString(wrapPlainDescription(line, "      ", width))
		} else {
			prose = append(prose, line)
		}
	}
	flush()
}

func fullFlag(flag cli.Flag) string {
	rendered := fileInputUsage(flag, flag.String())
	doc, ok := flag.(cli.DocGenerationFlag)
	if !ok {
		return rendered
	}
	placeholder := ""
	if doc.TakesValue() {
		placeholder = doc.TypeName()
		if placeholder == "" {
			placeholder = "value"
		}
	}
	_, quoted, found := strings.Cut(doc.GetUsage(), "`")
	if value, _, closed := strings.Cut(quoted, "`"); found && closed && value != "" {
		placeholder = value
	}
	heading, details, separated := strings.Cut(rendered, "\t")
	if !separated || heading != fullFlagNames(flag, placeholder) {
		// Preserve flags with their own presentation, such as --[no-]color.
		return rendered
	}
	// Keep descriptions, defaults, and environment hints after file-input wording.
	return fullFlagNames(flag, flagValueLabel(flag)) + "\t" + details
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
