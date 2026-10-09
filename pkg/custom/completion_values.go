package custom

import (
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

// configureCompletionValues binds suggestions to flag identity, not a shared
// name or help prose. Suggestions never restrict accepted request values.
func configureCompletionValues(root *cli.Command) {
	values := make(map[cli.Flag][]string)
	bind := func(command *cli.Command, name string, choices []string) {
		if command == nil {
			return
		}
		for _, flag := range command.Flags {
			if slices.Contains(flag.Names(), name) {
				values[flag] = slices.Clone(choices)
			}
		}
	}
	bind(root, "format", OutputFormats)
	bind(root, "format-error", OutputFormats)
	// Local command format contracts are narrower than API output formats.
	formats := func(command *cli.Command, choices ...string) {
		if command == nil {
			return
		}
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		overrides, _ := command.Metadata["completion-root-flag-values"].(map[string][]string)
		if overrides == nil {
			overrides = map[string][]string{}
		}
		overrides["format"] = choices
		command.Metadata["completion-root-flag-values"] = overrides
	}
	formats(root.Command("codex"), "auto", "text", "json")
	if tokenizer := root.Command("tokenizer"); tokenizer != nil {
		formats(tokenizer, "auto", "text")
		for _, name := range []string{"count", "inspect", "encodings", "licenses"} {
			formats(tokenizer.Command(name), "auto", "text", "json")
		}
	}
	if files := root.Command("files"); files != nil {
		// The upload contract uses FilePurpose, not FileObjectPurpose. Output
		// purposes belong only in list filters, never upload suggestions.
		// https://developers.openai.com/api/reference/resources/files/methods/create
		purposes := []string{
			string(openai.FilePurposeAssistants), string(openai.FilePurposeBatch),
			string(openai.FilePurposeEvals), string(openai.FilePurposeFineTune),
			string(openai.FilePurposeUserData), string(openai.FilePurposeVision),
		}
		bind(files.Command("create"), "purpose", purposes)
		bind(files.Command("upload"), "purpose", purposes)
		// List accepts a free-form purpose filter. Include known stored purposes.
		// https://developers.openai.com/api/reference/resources/files/methods/list
		purposes = append(purposes,
			string(openai.FileObjectPurposeAssistantsOutput),
			string(openai.FileObjectPurposeBatchOutput),
			string(openai.FileObjectPurposeFineTuneResults),
		)
		slices.Sort(purposes)
		bind(files.Command("list"), "purpose", purposes)
	}
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	root.Metadata["completion-flag-values"] = values
}
