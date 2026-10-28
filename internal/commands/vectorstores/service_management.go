package vectorstores

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// ManagementService supplies the remaining store, attachment, and batch operations.
type ManagementService interface {
	UpdateStore(context.Context, string, map[string]any) (*openai.VectorStore, error)
	DeleteStore(context.Context, string) error
	ListSourceFiles(context.Context, string) (SourcePage, error)
	AttachConfigured(context.Context, string, string, AttachOptions) (*openai.VectorStoreFile, error)
	CreateBatch(context.Context, string, []string, AttachOptions) (*openai.VectorStoreFileBatch, error)
	GetBatch(context.Context, string, string) (*openai.VectorStoreFileBatch, error)
	CancelBatch(context.Context, string, string) (*openai.VectorStoreFileBatch, error)
	ListBatchFiles(context.Context, string, string, string) (FilePage, error)
	UpdateFile(context.Context, string, string, map[string]any) (*openai.VectorStoreFile, error)
	RemoveFile(context.Context, string, string) error
	FileContent(context.Context, string, string) (string, error)
}

type SourcePage struct {
	Files []openai.FileObject
	Next  string
}

type AttachOptions struct {
	Attributes map[string]any `json:"attributes,omitempty"`
	Chunking   map[string]any `json:"chunking_strategy,omitempty"`
}

func (o AttachOptions) requestOptions() []option.RequestOption {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if o.Attributes != nil {
		opts = append(opts, option.WithJSONSet("attributes", o.Attributes))
	}
	if o.Chunking != nil {
		opts = append(opts, option.WithJSONSet("chunking_strategy", o.Chunking))
	}
	return opts
}

func (s *sdkService) UpdateStore(ctx context.Context, id string, changes map[string]any) (*openai.VectorStore, error) {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
	for key, value := range changes {
		opts = append(opts, option.WithJSONSet(key, value))
	}
	store, err := s.client.VectorStores.Update(ctx, id, openai.VectorStoreUpdateParams{}, opts...)
	if err == nil && (store == nil || store.ID != id) {
		err = workspaceFailure("The API returned an unexpected store. Refresh before retrying.", nil)
	}
	return store, err
}

func (s *sdkService) DeleteStore(ctx context.Context, id string) error {
	result, err := s.client.VectorStores.Delete(ctx, id, option.WithMaxRetries(0))
	if err == nil && (result == nil || result.ID != id || !result.Deleted) {
		err = workspaceFailure("Store deletion was not confirmed. Refresh stores before retrying.", nil)
	}
	return err
}

func (s *sdkService) ListSourceFiles(ctx context.Context, after string) (SourcePage, error) {
	params := openai.FileListParams{Limit: openai.Int(20), Purpose: openai.String("assistants"), Order: openai.FileListParamsOrderDesc}
	if after != "" {
		params.After = openai.String(after)
	}
	page, err := s.client.Files.List(ctx, params)
	if err != nil {
		return SourcePage{}, err
	}
	if page == nil || !page.JSON.Data.Valid() || !page.JSON.HasMore.Valid() {
		return SourcePage{}, workspaceFailure("The API returned an incomplete file list. Refresh before retrying.", nil)
	}
	last := ""
	for _, file := range page.Data {
		if file.ID == "" {
			return SourcePage{}, workspaceFailure("The API returned a file without an ID.", nil)
		}
		last = file.ID
	}
	next, err := nextCursor(after, last, page.HasMore)
	return SourcePage{Files: page.Data, Next: next}, err
}

func (s *sdkService) AttachConfigured(ctx context.Context, store, file string, opts AttachOptions) (*openai.VectorStoreFile, error) {
	result, err := s.client.VectorStores.Files.New(ctx, store, openai.VectorStoreFileNewParams{FileID: file}, opts.requestOptions()...)
	return checkAttachedFile(result, err, store, file)
}

func checkBatch(batch *openai.VectorStoreFileBatch, err error, store, id string) (*openai.VectorStoreFileBatch, error) {
	if err == nil && (batch == nil || batch.ID == "" || batch.VectorStoreID != store || id != "" && batch.ID != id) {
		err = workspaceFailure("The API returned an unexpected batch. Check Files before retrying.", nil)
	}
	return batch, err
}

func (s *sdkService) CreateBatch(ctx context.Context, store string, files []string, opts AttachOptions) (*openai.VectorStoreFileBatch, error) {
	batch, err := s.client.VectorStores.FileBatches.New(ctx, store, openai.VectorStoreFileBatchNewParams{FileIDs: files}, opts.requestOptions()...)
	return checkBatch(batch, err, store, "")
}

func (s *sdkService) GetBatch(ctx context.Context, store, id string) (*openai.VectorStoreFileBatch, error) {
	batch, err := s.client.VectorStores.FileBatches.Get(ctx, store, id)
	return checkBatch(batch, err, store, id)
}

func (s *sdkService) CancelBatch(ctx context.Context, store, id string) (*openai.VectorStoreFileBatch, error) {
	batch, err := s.client.VectorStores.FileBatches.Cancel(ctx, store, id, option.WithMaxRetries(0))
	return checkBatch(batch, err, store, id)
}

func (s *sdkService) ListBatchFiles(ctx context.Context, store, id, after string) (FilePage, error) {
	params := openai.VectorStoreFileBatchListFilesParams{Limit: openai.Int(20)}
	if after != "" {
		params.After = openai.String(after)
	}
	page, err := s.client.VectorStores.FileBatches.ListFiles(ctx, store, id, params)
	if err != nil {
		return FilePage{}, err
	}
	if page == nil || !page.JSON.Data.Valid() || !page.JSON.HasMore.Valid() {
		return FilePage{}, workspaceFailure("The API returned an incomplete batch file list.", nil)
	}
	last := ""
	for _, file := range page.Data {
		if file.ID == "" || file.VectorStoreID != "" && file.VectorStoreID != store {
			return FilePage{}, workspaceFailure("The API returned an unexpected batch file.", nil)
		}
		last = file.ID
	}
	next, err := nextCursor(after, last, page.HasMore)
	return FilePage{Files: page.Data, Next: next}, err
}

func (s *sdkService) UpdateFile(ctx context.Context, store, file string, attributes map[string]any) (*openai.VectorStoreFile, error) {
	result, err := s.client.VectorStores.Files.Update(ctx, store, file, openai.VectorStoreFileUpdateParams{},
		option.WithJSONSet("attributes", attributes), option.WithMaxRetries(0))
	return checkAttachedFile(result, err, store, file)
}

func (s *sdkService) RemoveFile(ctx context.Context, store, file string) error {
	result, err := s.client.VectorStores.Files.Delete(ctx, store, file, option.WithMaxRetries(0))
	if err == nil && (result == nil || result.ID != file || !result.Deleted) {
		err = workspaceFailure("Attachment removal was not confirmed. Refresh Files before retrying.", nil)
	}
	return err
}

func (s *sdkService) FileContent(ctx context.Context, store, file string) (string, error) {
	page, err := s.client.VectorStores.Files.Content(ctx, store, file)
	if err != nil {
		return "", err
	}
	if page == nil || !page.JSON.Data.Valid() {
		return "", workspaceFailure("The API returned incomplete file content.", nil)
	}
	var text strings.Builder
	for i, part := range page.Data {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if part.Type != "text" || !part.JSON.Text.Valid() {
			return "", workspaceFailure(fmt.Sprintf("The API returned unsupported file content at part %d.", i+1), nil)
		}
		if i > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(part.Text)
	}
	return text.String(), nil
}
