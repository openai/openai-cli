package vectorstores

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
)

func (s *sdkService) UploadFile(ctx context.Context, path string) (file *openai.FileObject, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	upload, err := openUpload(ctx, path)
	if err != nil {
		return nil, err
	}
	var body io.Closer
	options, err := request.MultipartRequestOptions(map[string]any{
		"file": upload, "purpose": "assistants",
	}, apiform.FormatBrackets, func(closer io.Closer) { body = closer })
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, body.Close())
	}()
	// MultipartRequestOptions streams once and disables SDK upload retries.
	file, err = s.client.Files.New(ctx, openai.FileNewParams{}, options...)
	if err == nil && (file == nil || file.ID == "") {
		err = workspaceFailure("The API returned no file ID. Check Files before uploading again.", nil)
	}
	return file, err
}

func openUpload(ctx context.Context, path string) (request.FileUpload, error) {
	// Open before validation, without waiting for a FIFO writer after replacement.
	// Never reopen the path after checking the owned descriptor.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return request.FileUpload{}, workspaceFailure("Could not read the selected file. Check its path and permissions.", err)
	}
	info, err := file.Stat()
	if err != nil {
		return request.FileUpload{}, errors.Join(workspaceFailure("Could not read the selected file. Check its path and permissions.", err), file.Close())
	}
	if !info.Mode().IsRegular() {
		return request.FileUpload{}, errors.Join(workspaceFailure("Choose a regular file to upload.", nil), file.Close())
	}
	if err := ctx.Err(); err != nil {
		return request.FileUpload{}, errors.Join(err, file.Close())
	}
	upload, err := request.FileUploadFromFile(file)
	if err != nil {
		return request.FileUpload{}, workspaceFailure("Could not read the selected file. Check its path and permissions.", err)
	}
	return upload, nil
}
