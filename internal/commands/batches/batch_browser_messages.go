package batches

import (
	"context"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

func batchBrowserActionLabel(action string) string {
	switch action {
	case "create":
		return "Create batch"
	case "output":
		return "Results"
	case "raw-output":
		return "API results"
	case "error":
		return "Request errors"
	case "input":
		return "Input requests"
	case "download":
		return "Save a file"
	case "save:output":
		return "Save output file"
	case "save:error":
		return "Save error file"
	case "save:input":
		return "Save input file"
	case "save:csv":
		return "Save results spreadsheet"
	case "status":
		return "Batch details"
	case "refresh":
		return "Refresh batch"
	default:
		return "Load batches"
	}
}

func batchBrowserContext(batch gjson.Result) string {
	return "Batch: " + batchInputLabel(batch.Get("id").Str) +
		"\nStatus: " + batchInputLabel(batchBrowserStatusLabel(batch.Get("status").Str))
}

func batchBrowserMissingFile(batch gjson.Result, selection string) (string, string) {
	title := "Results unavailable"
	if selection == "error" {
		title = "Error file unavailable"
	} else if selection == "input" {
		title = "Input file unavailable"
	}
	next := "Choose Batch details to inspect file IDs and status."
	if selection != "input" {
		switch batch.Get("status").Str {
		case "validating", "in_progress", "finalizing", "cancelling":
			next = "Press Ctrl+R in the action menu to refresh this batch."
		case "failed":
			if selection == "output" {
				next = "Choose View errors to inspect available diagnostics."
			}
		}
	}
	if selection == "error" && batchBrowserSuccessful(batch) {
		title = "No request errors reported"
		if file := batch.Get("output_file_id"); file.Type == gjson.String && file.Str != "" {
			next = "Choose View results to read the completed answers."
		}
	}
	return title, batchBrowserContext(batch) + "\n\nNext: " + next
}

// Browser sections preserve complete readable content for viewing and explicit printing.
func writeBatchBrowserSection(ctx context.Context, destination io.Writer, title, body string) error {
	return writeBatchStdout(ctx, destination, func(out io.Writer) error {
		style := lipgloss.NewStyle().Bold(isTerminal(destination) && os.Getenv("NO_COLOR") == "" && os.Getenv("FORCE_COLOR") != "0")
		text := "\n"
		if document, ok := destination.(*batchBrowserDocument); ok && !document.hasContent() {
			text = ""
		}
		if title != "" {
			text += style.Render(readable.Text(title)) + "\n\n"
		}
		if body != "" {
			text += readable.Text(body) + "\n"
		}
		n, err := io.WriteString(out, text)
		if err == nil && n != len(text) {
			return io.ErrShortWrite
		}
		return err
	})
}
