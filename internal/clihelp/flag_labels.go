package clihelp

import (
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// FlagLabel supplies a semantic value label only for flags declared by Owner.
type FlagLabel struct {
	Owner *cli.Command
	Names []string
	Label string
}

func commandFlagValueLabel(command *cli.Command, flag cli.Flag) string {
	labels, _ := command.Metadata["help-flag-labels"].([]FlagLabel)
	for _, label := range labels {
		if label.Owner == nil || !slices.Contains(label.Owner.Flags, flag) {
			continue
		}
		for _, name := range label.Names {
			if slices.Contains(flag.Names(), name) {
				return label.Label
			}
		}
	}
	return flagValueLabel(flag)
}

// Labels describe accepted input without reading configured values or defaults.
func flagValueLabel(flag cli.Flag) string {
	doc, ok := flag.(cli.DocGenerationFlag)
	if !ok || !doc.TakesValue() {
		return ""
	}
	if input, ok := flag.(interface{ IsFileInput() bool }); ok && input.IsFileInput() {
		return "PATH"
	}
	fileFlag := flag
	if wrapped, ok := flag.(interface{ CLIStringFlag() *cli.StringFlag }); ok {
		fileFlag = wrapped.CLIStringFlag()
	}
	if value, ok := fileFlag.(*cli.StringFlag); ok && value != nil && value.TakesFile {
		return "PATH"
	}
	if doc.TypeName() == "string" {
		if names := flag.Names(); len(names) > 0 {
			switch names[0] {
			case "model":
				return "MODEL"
			case "format", "format-error":
				return "FORMAT"
			case "transform", "transform-error":
				return "PATH"
			case "output-dir":
				return "DIRECTORY"
			case "base-url":
				return "URL"
			case "prompt", "instructions":
				return "TEXT"
			}
		}
	}
	return flagTypeLabel(doc.TypeName())
}

func flagTypeLabel(name string) string {
	if key, value, ok := strings.Cut(name, "="); ok {
		return flagTypeLabel(key) + "=" + flagTypeLabel(value)
	}
	switch name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "INTEGER"
	case "float", "float32", "float64":
		return "NUMBER"
	case "bool", "boolean":
		return "BOOLEAN"
	case "string":
		return "TEXT"
	case "date":
		return "DATE"
	case "datetime":
		return "DATETIME"
	case "time":
		return "TIME"
	default:
		return "VALUE"
	}
}
