package vectorstores

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/tidwall/gjson"
)

// StorePage and FilePage retain the cursor for an explicit Load more action.
type StorePage struct {
	Stores []openai.VectorStore
	Next   string
}

type FilePage struct {
	Files []openai.VectorStoreFile
	Next  string
}

// SearchPage reports continuation without inventing an unsupported API input.
// The current search request has no cursor, although its response can report more.
type SearchPage struct {
	Results []openai.VectorStoreSearchResponse
	More    bool
}

// Service separates request execution from terminal input and workspace state.
// Upload and attachment stay separate so a failed attachment retains its file ID.
type Service interface {
	ListStores(context.Context, string) (StorePage, error)
	GetStore(context.Context, string) (*openai.VectorStore, error)
	CreateStore(context.Context, string) (*openai.VectorStore, error)
	ListFiles(context.Context, string, string) (FilePage, error)
	UploadFile(context.Context, string) (*openai.FileObject, error)
	AttachFile(context.Context, string, string) (*openai.VectorStoreFile, error)
	GetFile(context.Context, string, string) (*openai.VectorStoreFile, error)
	Search(context.Context, string, string) (SearchPage, error)
}

type sdkService struct{ client openai.Client }

// NewService uses the client's existing authentication, transport, and request policies.
func NewService(client openai.Client) Service { return &sdkService{client: client} }

// The SDK decodes numeric and boolean fields into strings. Only trust IDs sent as strings.
func validResponseID(field respjson.Field) bool {
	raw := field.Raw()
	return strings.HasPrefix(raw, `"`) && raw != `""`
}

func (s *sdkService) ListStores(ctx context.Context, after string) (StorePage, error) {
	params := openai.VectorStoreListParams{Limit: openai.Int(20), Order: openai.VectorStoreListParamsOrderDesc}
	if after != "" {
		params.After = openai.String(after)
	}
	page, err := s.client.VectorStores.List(ctx, params)
	if err != nil {
		return StorePage{}, err
	}
	if page == nil || !page.JSON.Data.Valid() || !page.JSON.HasMore.Valid() {
		return StorePage{}, workspaceFailure("The API returned an incomplete store list. Refresh stores to try again.", nil)
	}
	result := StorePage{Stores: page.Data}
	var last string
	for _, store := range page.Data {
		if !validResponseID(store.JSON.ID) {
			return result, workspaceFailure("The API returned a store without an ID. Refresh stores to try again.", nil)
		}
		last = store.ID
	}
	result.Next, err = nextCursor(after, last, page.HasMore)
	return result, err
}

func (s *sdkService) GetStore(ctx context.Context, id string) (*openai.VectorStore, error) {
	store, err := s.client.VectorStores.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if store == nil || !validResponseID(store.JSON.ID) || store.ID != id {
		return nil, workspaceFailure("The API returned an unexpected store. Refresh stores to try again.", nil)
	}
	return store, nil
}

func (s *sdkService) CreateStore(ctx context.Context, name string) (*openai.VectorStore, error) {
	store, err := s.client.VectorStores.New(ctx, openai.VectorStoreNewParams{Name: openai.String(name)}, option.WithMaxRetries(0))
	if err != nil {
		return nil, err
	}
	if store == nil || !validResponseID(store.JSON.ID) {
		return nil, workspaceFailure("The API returned no store ID. Refresh stores before creating another store.", nil)
	}
	return store, nil
}

func (s *sdkService) ListFiles(ctx context.Context, storeID, after string) (FilePage, error) {
	params := openai.VectorStoreFileListParams{Limit: openai.Int(20), Order: openai.VectorStoreFileListParamsOrderDesc}
	if after != "" {
		params.After = openai.String(after)
	}
	page, err := s.client.VectorStores.Files.List(ctx, storeID, params)
	if err != nil {
		return FilePage{}, err
	}
	if page == nil || !page.JSON.Data.Valid() || !page.JSON.HasMore.Valid() {
		return FilePage{}, workspaceFailure("The API returned an incomplete file list. Refresh files to try again.", nil)
	}
	result := FilePage{Files: page.Data}
	var last string
	for _, file := range page.Data {
		if !validAttachmentID(file, storeID) {
			return result, workspaceFailure("The API returned an unexpected file. Refresh files to try again.", nil)
		}
		last = file.ID
	}
	result.Next, err = nextCursor(after, last, page.HasMore)
	return result, err
}

func nextCursor(after, last string, more bool) (string, error) {
	if !more {
		return "", nil
	}
	if last == "" || last == after {
		return "", workspaceFailure("The API returned an invalid page cursor. Refresh the list to try again.", nil)
	}
	return last, nil
}

func (s *sdkService) AttachFile(ctx context.Context, storeID, fileID string) (*openai.VectorStoreFile, error) {
	file, err := s.client.VectorStores.Files.New(ctx, storeID, openai.VectorStoreFileNewParams{
		FileID: fileID,
	}, option.WithMaxRetries(0))
	return checkAttachedFile(file, err, storeID, fileID)
}

func (s *sdkService) GetFile(ctx context.Context, storeID, fileID string) (*openai.VectorStoreFile, error) {
	file, err := s.client.VectorStores.Files.Get(ctx, storeID, fileID)
	return checkAttachedFile(file, err, storeID, fileID)
}

func checkAttachedFile(file *openai.VectorStoreFile, err error, storeID, fileID string) (*openai.VectorStoreFile, error) {
	if err != nil {
		return nil, err
	}
	if file == nil || !validAttachmentID(*file, storeID) || file.ID != fileID {
		return nil, workspaceFailure("The API returned an unexpected attachment. Refresh files to check its status.", nil)
	}
	return file, nil
}

func validAttachmentID(file openai.VectorStoreFile, storeID string) bool {
	return validResponseID(file.JSON.ID) &&
		(file.VectorStoreID == "" || validResponseID(file.JSON.VectorStoreID) && file.VectorStoreID == storeID)
}

func (s *sdkService) Search(ctx context.Context, storeID, query string) (SearchPage, error) {
	page, err := s.client.VectorStores.Search(ctx, storeID, openai.VectorStoreSearchParams{
		Query:         openai.VectorStoreSearchParamsQueryUnion{OfString: openai.String(query)},
		MaxNumResults: openai.Int(10),
	})
	if err != nil {
		return SearchPage{}, err
	}
	if page == nil || !page.JSON.Data.Valid() {
		return SearchPage{}, workspaceFailure("The API returned incomplete search results. Submit your search to try again.", nil)
	}
	for _, result := range page.Data {
		if err := ValidateSearchResult(result); err != nil {
			return SearchPage{}, err
		}
	}
	raw := page.RawJSON()
	return SearchPage{
		Results: page.Data,
		More:    gjson.Get(raw, "has_more").Bool() || gjson.Get(raw, "next_page").String() != "",
	}, nil
}
