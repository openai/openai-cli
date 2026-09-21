package cmd

import "github.com/openai/openai-cli/pkg/custom"

// Preserve the runtime types exposed by this package before the runtime moved to pkg/custom.
type FileEmbedStyle = custom.FileEmbedStyle
type FilePathValue = custom.FilePathValue

const (
	EmbedText     = custom.EmbedText
	EmbedIOReader = custom.EmbedIOReader
)
