package app

import (
	"slices"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Only a declared numeric query parameter on a pageable command can set its API page size.
// A body field named limit or a plain CLI flag can have a different meaning.
type listPageSizeFlag interface {
	cli.Flag
	requestflag.InRequest
}

type listPageFlags struct {
	size    listPageSizeFlag
	cursors []string
	hasCap  bool
}

func pageFlags(command *cli.Command) listPageFlags {
	var flags listPageFlags
	for _, flag := range command.Flags {
		if slices.Contains(flag.Names(), "max-items") {
			flags.hasCap = true
		}
		binding, ok := flag.(listPageSizeFlag)
		if !ok {
			continue
		}
		switch binding.GetQueryPath() {
		case "limit", "page_size", "per_page":
			switch flag.(type) {
			case *requestflag.Flag[int64], *requestflag.Flag[*int64]:
				flags.size = binding
			}
		case "after", "before", "cursor", "page", "page_token", "offset":
			flags.cursors = append(flags.cursors, flag.Names()[0])
		}
	}
	if !flags.hasCap && len(flags.cursors) == 0 {
		flags.size = nil
	}
	return flags
}

// The generated flags identify the wire parameter but do not carry numeric schema bounds.
// Canonical resource names keep promoted and colon-form aliases on the same API contract.
// A new pageable resource inherits screen sizing without requiring a table entry.
func listPageMaximum(resource string) int {
	switch resource {
	case "files":
		return 10_000
	case "admin organization groups", "admin organization groups users", "admin organization groups roles",
		"admin organization roles", "admin organization users roles", "admin organization projects roles",
		"admin organization projects groups roles", "admin organization projects users roles",
		"admin organization projects rate-limits":
		return 1_000
	case "audio voice-consents", "batches", "beta assistants", "beta chatkit threads",
		"beta responses input-items", "beta threads messages", "beta threads runs", "beta threads runs steps",
		"containers", "containers files", "conversations items", "responses input-items",
		"skills", "skills versions", "vector-stores", "vector-stores file-batches", "vector-stores files",
		"videos", "webhooks",
		"admin organization admin-api-keys", "admin organization audit-logs", "admin organization certificates",
		"admin organization external-keys", "admin organization external-storage", "admin organization invites",
		"admin organization projects", "admin organization projects api-keys", "admin organization projects certificates",
		"admin organization projects groups", "admin organization projects service-accounts",
		"admin organization projects spend-alerts", "admin organization projects users",
		"admin organization spend-alerts", "admin organization users":
		return 100
	default:
		return 0
	}
}
