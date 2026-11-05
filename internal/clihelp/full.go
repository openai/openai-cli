package clihelp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
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
	if strings.TrimSpace(usage) == "" {
		body, ok := flag.(interface{ GetBodyPath() string })
		names := flag.Names()
		if ok && body.GetBodyPath() == "model" && len(names) > 0 && names[0] == "model" {
			return pathInputDescription("model")
		}
	}
	return usage
}

func fullFlagGroups(command *cli.Command, flags []cli.Flag, width int) string {
	return renderFlagGroups(command, flags, width, false)
}

func compactFlagGroups(command *cli.Command, flags []cli.Flag, width int) string {
	return renderFlagGroups(command, flags, width, true)
}

func renderFlagGroups(command *cli.Command, flags []cli.Flag, width int, compact bool) string {
	style := commandHelpStyle(command)
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
	if options := grouped["Other options"]; len(grouped) == 1 && len(options) > 0 {
		if compact {
			return compactFlagIndex(command, options, "  ", width, style)
		}
		if rows := compactFlagList(command, options, width, style); rows != "" {
			return rows
		}
		return flagList(command, options, width)
	}
	var out strings.Builder
	for _, group := range groups {
		flags := grouped[group.Title]
		if len(flags) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "  %s\n", style.render(style.section, group.Title))
		switch {
		case !compact:
			out.WriteString(flagList(command, flags, width))
		case group.Title == "Required inputs":
			for _, flag := range flags {
				heading, description := flagReference(command, flag)
				writeFlagLabels(&out, styledFlagLabels(heading, style), "    ", width)
				out.WriteString(wrapPlainDescription(shortDescription(description), "      ", width))
			}
		default:
			out.WriteString(compactFlagIndex(command, flags, "    ", width, style))
		}
		delete(grouped, group.Title)
	}
	return out.String()
}

func compactFlagIndex(command *cli.Command, flags []cli.Flag, indent string, width int, style helpStyle) string {
	var labels []string
	for _, flag := range flags {
		heading, _ := flagReference(command, flag)
		labels = append(labels, styledFlagLabels(heading, style)...)
	}
	var out strings.Builder
	writeFlagLabels(&out, labels, indent, width)
	return out.String()
}

// Use the navigation layout only when every flag fits on one line. Keep
// metadata, links and longer explanations in the complete reference layout.
func compactFlagList(command *cli.Command, flags []cli.Flag, width int, style helpStyle) string {
	headings, descriptions := make([]string, len(flags)), make([]string, len(flags))
	longestDescription := 0
	for i, flag := range flags {
		heading, description := flagReference(command, flag)
		if strings.ContainsAny(heading+description, "\n\t") || strings.Contains(description, "://") ||
			strings.HasPrefix(description, "Default:") || strings.HasPrefix(description, "Env:") {
			return ""
		}
		headings[i], descriptions[i] = heading, description
		longestDescription = max(longestDescription, ansi.StringWidth(description))
	}
	column := helpDescriptionColumn(width, headings)
	if column == 0 || column+longestDescription > width {
		return ""
	}
	var out strings.Builder
	for i, heading := range headings {
		label := strings.Join(styledFlagLabels(heading, style), ", ")
		out.WriteString(style.entry(heading, descriptions[i], label, width, column))
	}
	return out.String()
}

func styledFlagLabels(heading string, style helpStyle) []string {
	labels := strings.Split(heading, ", ")
	for i, label := range labels {
		name, value, hasValue := strings.Cut(label, "=")
		if hasValue && value == "" {
			labels[i] = style.render(style.section, label)
		} else {
			labels[i] = style.flag(name, value)
		}
	}
	return labels
}

func flagList(command *cli.Command, flags []cli.Flag, width int) string {
	style := commandHelpStyle(command)
	var out strings.Builder
	for i, flag := range flags {
		if i > 0 {
			out.WriteByte('\n')
		}
		heading, description := flagReference(command, flag)
		writeFlagLabels(&out, styledFlagLabels(heading, style), "    ", width)
		writeFlagDescription(&out, description, width, style)
	}
	return out.String()
}

// Wrap only between complete labels, including aliases and compound values.
func writeFlagLabels(out *strings.Builder, labels []string, indent string, width int) {
	out.WriteString(indent)
	column := len(indent)
	for i, label := range labels {
		if i > 0 {
			if column+2+ansi.StringWidth(label) > width {
				if column < width {
					out.WriteByte(',')
				}
				out.WriteString("\n" + indent)
				column = len(indent)
			} else {
				out.WriteString(", ")
				column += 2
			}
		}
		out.WriteString(label)
		column += ansi.StringWidth(label)
	}
	out.WriteByte('\n')
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
		if label := commandFlagValueLabel(command, flag); label != "" {
			name += "=" + label
		}
		names = append(names, prefix+name)
	}
	heading = strings.Join(names, ", ")
	usage := fileInputUsage(flag, commandFlagUsage(command, flag, doc.GetUsage()))
	if multi, ok := flag.(cli.DocGenerationMultiValueFlag); ok && multi.IsMultiValueFlag() &&
		!strings.Contains(strings.ToLower(usage), "repeat") {
		usage += "\nCan be used more than once."
	}
	// Generated descriptions often end in a newline. Metadata supplies its own.
	usage = strings.TrimRight(usage, "\r\n")
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

func writeFlagDescription(out *strings.Builder, description string, width int, style helpStyle) {
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
			out.WriteString(style.render(style.note, wrapPlainDescription(line, "      ", width)))
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
