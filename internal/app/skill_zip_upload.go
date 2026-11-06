package app

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

func isSkillCreate(cmd *cli.Command) bool {
	resource := commands.ResourceName(cmd)
	return cmd.Name == "create" && (resource == "skills" || resource == "skills:versions")
}

// The CLI collects --files into a slice. The API takes one ZIP as a single
// multipart field named "files", separate from the individual-file array.
func selectSkillZIP(cmd *cli.Command, body map[string]any) (request.BodyContentType, error) {
	value := body["files"]
	if values, ok := value.([]any); ok && len(values) == 1 {
		value = values[0]
	}
	path, ok := value.(request.FilePathValue)
	if !ok || (!request.IsStdinPath(string(path)) && !strings.EqualFold(filepath.Ext(string(path)), ".zip")) {
		return request.ApplicationJSON, nil
	}
	// The SDK rejects an empty version path before taking ownership of the upload.
	if cmd.IsSet("skill-id") && cmd.String("skill-id") == "" {
		return request.ApplicationJSON, &commands.LocalError{Message: "Supply a nonempty --skill-id for the new version."}
	}
	body["files"] = path
	return request.MultipartFormEncoded, nil
}

func labelSkillZIP(body map[string]any) error {
	switch file := body["files"].(type) {
	case request.FileUpload:
		file.MediaType = "application/zip"
		body["files"] = file
	case io.Reader: // stdin has no filename
		body["files"] = request.FileUpload{Reader: file, FileName: "skill.zip", MediaType: "application/zip"}
	}
	return nil
}
