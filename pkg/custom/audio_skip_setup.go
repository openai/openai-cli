package custom

import (
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// Classify local audio paths synchronously from the current declarations. This
// does not parse values, invoke callbacks, cache nodes, or retain a command tree
// for the optional setup worker. Unrecognized prefixes keep the existing policy.
func skipAudioFirstRunSetup(root *cli.Command, args []string) bool {
	if root == nil || len(args) < 2 {
		return false
	}
	audio := root.Command("audio")
	if audio == nil {
		return false
	}
	current := root
	lineage := []*cli.Command{root}
	var helpLineage []*cli.Command
	flags, help := audioSetupParsesFlags(current), false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if flags && strings.TrimSpace(arg) == "--" {
			flags = false
			continue
		}
		if flags && strings.HasPrefix(strings.TrimSpace(arg), "-") {
			token := strings.TrimPrefix(strings.TrimSpace(arg), "-")
			token = strings.TrimPrefix(token, "-")
			name, value, assigned := strings.Cut(token, "=")
			owners := lineage
			if helpLineage != nil {
				owners = helpLineage
			}
			flag := audioSetupFlag(owners, name)
			shape, ok := flag.(interface{ TakesValue() bool })
			if !ok {
				return false
			}
			if shape.TakesValue() {
				if !assigned {
					index++
					if index >= len(args) {
						return false
					}
				}
			} else if assigned && value != "" {
				if _, err := strconv.ParseBool(value); err != nil {
					return false
				}
			}
			continue
		}
		if arg == "help" && !help {
			help = true
			if declared := root.Command("help"); declared != nil {
				helpLineage = []*cli.Command{root, declared}
				flags = audioSetupParsesFlags(declared)
			} else {
				flags = audioSetupParsesFlags(current)
			}
			continue
		}
		child := current.Command(arg)
		if child == nil {
			return false
		}
		if current == root && child != audio {
			return false
		}
		if current == audio && child.Metadata[localAudioMetadata] == true {
			return true
		}
		current = child
		lineage = append(lineage, child)
		// Each dispatched command starts its own parser, including after '--'.
		if !help {
			flags = audioSetupParsesFlags(child)
		}
	}
	return false
}

func audioSetupParsesFlags(command *cli.Command) bool {
	return !command.SkipFlagParsing && (command.StopOnNthArg == nil || *command.StopOnNthArg != 0)
}

func audioSetupDeclaredFlags(command *cli.Command) []cli.Flag {
	flags := append([]cli.Flag(nil), command.Flags...)
	for _, group := range command.MutuallyExclusiveFlags {
		for _, alternatives := range group.Flags {
			flags = append(flags, alternatives...)
		}
	}
	// The CLI adds its declared help flag during startup. Read its metadata
	// without running setupCommandGraph or mutating these command declarations.
	if !command.HideHelp && cli.HelpFlag != nil {
		flags = append(flags, cli.HelpFlag)
	}
	return flags
}

func audioSetupFlag(lineage []*cli.Command, name string) cli.Flag {
	local := audioSetupDeclaredFlags(lineage[len(lineage)-1])
	for _, flag := range local {
		if flag != nil && slices.Contains(flag.Names(), name) {
			return flag
		}
	}
	for index := len(lineage) - 2; index >= 0; index-- {
		for _, flag := range audioSetupDeclaredFlags(lineage[index]) {
			persistent, ok := flag.(cli.LocalFlag)
			if !ok || persistent.IsLocal() {
				continue
			}
			names := flag.Names()
			if !slices.Contains(names, name) {
				continue
			}
			// The parser suppresses an ancestor flag as a whole when any of its
			// aliases belongs to the current command, not only the queried alias.
			shadowed := false
			for _, current := range local {
				if current == nil {
					continue
				}
				for _, alias := range current.Names() {
					if slices.Contains(names, alias) {
						shadowed = true
					}
				}
			}
			if !shadowed {
				return flag
			}
		}
	}
	return nil
}
