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
		if quietOutput(opts.Context) {
			// Quiet suppresses the receipt, while ordinary rendering keeps data.
			return false, nil
		}
		if !validFileReceipt(value) {
			return false, nil
		}
		invocation, _ := opts.Context.Value(fileInvocationKey{}).(fileInvocation)
		return true, writeFileReceipt(out, value, imagePickerParentShell(opts.Context), invocation)
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

func writeFileReceipt(out io.Writer, value gjson.Result, shell string, invocation fileInvocation) error {
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
	if shell == "" || invocation.omitHint || strings.ContainsRune(id, 0) || !utf8.ValidString(id) {
		return nil
	}
	// Reuse the existing shell quoting contracts, including control characters.
	args := []string{"files", "get", id}
	if strings.HasPrefix(id, "-") {
		args = []string{"files", "get", "--file-id=" + id}
	}
	if value.Get("status").Str == "error" {
		args = append(args, "--format", "json")
	}
	// The next workflow step is read-only. Never suggest a destination that could
	// overwrite the source or another file when the command is copied later.
	args = append(append([]string{}, invocation.requestArgs...), args...)
	if command := formatImagePickerCommand(args, shell); command != "" {
		prefix := fileReceiptInvocation(invocation, shell)
		if prefix == "" {
			return nil
		}
		command = prefix + strings.TrimPrefix(command, "openai")
		return readable.WriteText(out, "\nInspect it: "+command)
	}
	return nil
}

// Keep help's executable selection, using the actual shell for quoted paths.
func fileReceiptInvocation(invocation fileInvocation, shell string) string {
	if !utf8.ValidString(invocation.executable) {
		return ""
	}
	if invocation.display == "" {
		return "openai"
	}
	plain, _ := imagePickerQuoteProperties(invocation.display)
	if plain || invocation.goRun || invocation.executable == "" {
		return invocation.display
	}
	quote := imagePickerShellQuoter(shell)
	if quote == nil {
		return ""
	}
	command := quote(invocation.executable)
	if shell == "pwsh" {
		command = "& " + command
	}
	return command
}
