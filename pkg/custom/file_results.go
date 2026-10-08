package custom

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// Only friendly file routes opt into terminal presentation. Downloads never do.
func showFileResult(value gjson.Result, opts ShowJSONOpts) (bool, error) {
	opts.setDefaults()
	command, _ := opts.Context.Value(fileCommandKey{}).(string)
	if command == "" || opts.OutputKind != OutputResponse || opts.Transform != "" || opts.RawOutput ||
		resolvedOutputFormat(opts) != "text" || !isTerminal(opts.Stdout) || !isTerminal(opts.Stderr) {
		return false, nil
	}
	if err := opts.Context.Err(); err != nil {
		return true, err
	}
	out := outputWriter{ctx: opts.Context, out: opts.Stdout}
	switch {
	case command == fileGetCommand && opts.Operation == "(resource) files > (method) retrieve":
		metadata, err := transformers.ProjectFileMetadata(opts.Context, value)
		if err != nil {
			return true, err
		}
		if err := readable.WriteText(out, "File metadata"); err != nil {
			return true, err
		}
		return true, readable.Write(out, metadata)
	case command == fileUploadCommand && opts.Operation == "(resource) files > (method) create":
		if !validFileReceipt(value) {
			return false, nil
		}
		return true, writeFileReceipt(out, value, imagePickerParentShell(opts.Context))
	default:
		return false, nil
	}
}

func validFileReceipt(value gjson.Result) bool {
	if !value.IsObject() || !gjson.Valid(value.Raw) || value.Get("object").Str != "file" {
		return false
	}
	for _, name := range []string{"id", "filename", "purpose"} {
		field := value.Get(name)
		if field.Type != gjson.String || field.Str == "" || !utf8.ValidString(field.Str) {
			return false
		}
	}
	if status := value.Get("status"); status.Exists() && status.Type != gjson.Null {
		if status.Type != gjson.String {
			return false
		}
		switch status.Str {
		case "uploaded", "processed", "error":
		default:
			return false
		}
	}
	if bytes := value.Get("bytes"); bytes.Exists() && bytes.Type != gjson.Null {
		if bytes.Type != gjson.Number || strings.IndexFunc(bytes.Raw, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return false
		}
	}
	// Fall back to full output for new fields or ambiguous duplicate values.
	seen := map[string]bool{}
	valid := true
	value.ForEach(func(key, field gjson.Result) bool {
		switch key.Str {
		case "id", "object", "filename", "purpose", "bytes", "created_at", "expires_at", "status", "status_details":
			valid = !seen[key.Str]
		default:
			valid = false
		}
		seen[key.Str] = true
		return valid
	})
	return valid
}

func writeFileReceipt(out io.Writer, value gjson.Result, shell string) error {
	// Keep each response value on one line before adding our own layout.
	safe := func(text string) string { return readable.Text(jsonview.SanitizeTerminalString(text)) }
	filename, id := value.Get("filename").Str, value.Get("id").Str
	size := ""
	if bytes := value.Get("bytes"); bytes.Type == gjson.Number {
		size = " (" + bytes.Raw + " B)"
	}
	if _, err := fmt.Fprintf(out, "Uploaded %s%s\nID: %s\nPurpose: %s\n", safe(filename), size, safe(id), safe(value.Get("purpose").Str)); err != nil {
		return err
	}
	if status := value.Get("status"); status.Str == "error" {
		if err := readable.WriteText(out, "Status: error"); err != nil {
			return err
		}
	}
	if details := value.Get("status_details"); details.Exists() && details.Type != gjson.Null && details.String() != "" {
		if err := readable.WriteText(out, "Status details: "+safe(details.String())); err != nil {
			return err
		}
	}
	if shell == "" || strings.ContainsRune(id, 0) || !utf8.ValidString(id) {
		return nil
	}
	verb, label := "download", "Download it: "
	if value.Get("status").Str == "error" {
		verb, label = "get", "Inspect it: "
	}
	// Reuse the existing shell quoting contracts, including control characters.
	args := []string{"files", verb, id}
	if strings.HasPrefix(id, "-") {
		args = []string{"files", verb, "--file-id=" + id}
	}
	if verb == "get" {
		args = append(args, "--format", "json")
	} else {
		if !utf8.ValidString(filename) || strings.ContainsRune(filename, 0) || strings.ContainsAny(filename, `/\`) || filename == "." || filename == ".." {
			return nil
		}
		if filename == "-" {
			filename = "./-"
		}
		args = append(args, "--output", filename)
	}
	if command := formatImagePickerCommand(args, shell); command != "" {
		return readable.WriteText(out, "\n"+label+command)
	}
	return nil
}
