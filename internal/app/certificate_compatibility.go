package app

import (
	"slices"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Preserve the historical hidden flag as a separate request field, not an alias
// for the required certificate field. Keep this CLI compatibility outside codegen.
func configureCertificateCompatibility(root *cli.Command) {
	certificates := root.Command("admin:organization:certificates")
	if certificates == nil {
		return
	}
	create := certificates.Command("create")
	if create == nil {
		return
	}
	for _, flag := range create.Flags {
		if slices.Contains(flag.Names(), "content") {
			return
		}
	}
	for i, flag := range create.Flags {
		if slices.Contains(flag.Names(), "certificate") {
			create.Flags = slices.Insert(create.Flags, i+1, cli.Flag(&requestflag.Flag[string]{
				Name: "content", Hidden: true, BodyPath: "content",
			}))
			return
		}
	}
}
