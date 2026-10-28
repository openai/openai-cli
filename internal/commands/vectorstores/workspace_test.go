package vectorstores

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelService struct {
	listStores  func(context.Context, string) (StorePage, error)
	getStore    func(context.Context, string) (*openai.VectorStore, error)
	createStore func(context.Context, string) (*openai.VectorStore, error)
	listFiles   func(context.Context, string, string) (FilePage, error)
	upload      func(context.Context, string) (*openai.FileObject, error)
	attach      func(context.Context, string, string) (*openai.VectorStoreFile, error)
	getFile     func(context.Context, string, string) (*openai.VectorStoreFile, error)
	search      func(context.Context, string, string) (SearchPage, error)
}

func (s modelService) ListStores(ctx context.Context, after string) (StorePage, error) {
	return s.listStores(ctx, after)
}
func (s modelService) GetStore(ctx context.Context, id string) (*openai.VectorStore, error) {
	return s.getStore(ctx, id)
}
func (s modelService) CreateStore(ctx context.Context, name string) (*openai.VectorStore, error) {
	return s.createStore(ctx, name)
}
func (s modelService) ListFiles(ctx context.Context, id, after string) (FilePage, error) {
	return s.listFiles(ctx, id, after)
}
func (s modelService) UploadFile(ctx context.Context, path string) (*openai.FileObject, error) {
	return s.upload(ctx, path)
}
func (s modelService) AttachFile(ctx context.Context, store, file string) (*openai.VectorStoreFile, error) {
	return s.attach(ctx, store, file)
}
func (s modelService) GetFile(ctx context.Context, store, file string) (*openai.VectorStoreFile, error) {
	return s.getFile(ctx, store, file)
}
func (s modelService) Search(ctx context.Context, store, query string) (SearchPage, error) {
	return s.search(ctx, store, query)
}

func modelForTest(t *testing.T, service Service) *workspace {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	m := newWorkspace(ctx, service, func(err error) string { return err.Error() })
	m.color = false
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() { m.shutdown(cancel) })
	return m
}

func modelKey(m *workspace, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func modelComplete(t *testing.T, m *workspace, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	require.NotNil(t, cmd, "expected a service command")
	msg := cmd()
	require.IsType(t, workMessage{}, msg)
	_, next := m.Update(msg)
	return next
}

func TestWorkspaceCreateFromKeys(t *testing.T) {
	calls := 0
	m := modelForTest(t, modelService{
		listStores: func(context.Context, string) (StorePage, error) { return StorePage{}, nil },
		createStore: func(_ context.Context, name string) (*openai.VectorStore, error) {
			calls++
			assert.Equal(t, "demo $(literal)", name)
			return &openai.VectorStore{ID: "vs_created", Name: name}, nil
		},
	})
	modelComplete(t, m, m.loadStores(false))
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "create", m.page)
	m.Update(tea.PasteMsg{Content: "demo $(literal)"})
	require.Zero(t, calls, "editing must not create a store")
	modelKey(m, tea.KeyEnter)
	require.False(t, m.busy, "the first Enter selects the create action")
	cmd := modelKey(m, tea.KeyEnter)
	require.True(t, m.busy)
	require.Nil(t, modelKey(m, tea.KeyEnter), "repeated Enter must not submit during a request")
	modelComplete(t, m, cmd)
	assert.Equal(t, 1, calls)
	require.NotNil(t, m.store)
	assert.Equal(t, "vs_created", m.store.ID)
	assert.Equal(t, "home", m.page)
	assert.Empty(t, m.editor.Value(), "the store name must not become a search question")
	assert.Equal(t, "vs_created", m.stores[0].ID)
}

func TestWorkspaceLargePassageRetainsTailAndExits(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	text := strings.Repeat("a complete synthetic passage with searchable content\n", 40000) + "unique final passage"
	m.results = []openai.VectorStoreSearchResponse{{
		Filename: "large.txt",
		Content:  []openai.VectorStoreSearchResponseContent{{Type: "text", Text: text}},
	}}
	m.enter("home")
	m.focus = 6
	modelKey(m, tea.KeyEnter)
	require.Equal(t, text, m.passage)
	require.Greater(t, len(m.passageLines), 40000)
	layout := &m.passageLines[0]
	modelKey(m, tea.KeyEnd)
	for i := 0; i < 10; i++ {
		assert.Contains(t, m.View().Content, "unique final passage")
		assert.Same(t, layout, &m.passageLines[0])
	}
	modelKey(m, tea.KeyUp)
	modelKey(m, tea.KeyDown)
	assert.Contains(t, m.View().Content, "unique final passage")
	m.note = strings.Repeat("retained upload error ", 10)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	modelKey(m, tea.KeyEnd)
	assert.Contains(t, m.View().Content, "unique final passage")
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "home", m.page)
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.True(t, m.quit)
	assert.Equal(t, 130, m.exitCode)
}

func TestWorkspaceWatchedFileFailureIsNotSuccess(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "unexpected"} {
		t.Run(status, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.store = &openai.VectorStore{ID: "vs_demo"}
			m.page = "files"
			m.pendingFile = "file_retained"
			m.files = []openai.VectorStoreFile{{ID: "file_watched", Status: "in_progress"}}
			m.Update(workMessage{kind: "watch", attached: &openai.VectorStoreFile{
				ID: "file_watched", Status: openai.VectorStoreFileStatus(status),
			}})
			assert.Error(t, m.lastErr)
			assert.Equal(t, "file_retained", m.pendingFile)
			assert.Equal(t, status, string(m.files[0].Status))
		})
	}
}

func TestWorkspaceSearchAndReadPassageFromKeys(t *testing.T) {
	query := "@notes how many ribbons? $(literal)"
	m := modelForTest(t, modelService{
		search: func(_ context.Context, id, gotQuery string) (SearchPage, error) {
			assert.Equal(t, "vs_demo", id)
			assert.Equal(t, query, gotQuery)
			return SearchPage{
				More: true,
				Results: []openai.VectorStoreSearchResponse{{
					FileID: "file_demo", Filename: "kite.txt", Score: 0.99,
					Content: []openai.VectorStoreSearchResponseContent{
						{Type: "text", Text: "The kite has seven ribbons."},
						{Type: "text", Text: "The ribbons are blue."},
					},
				}},
			}, nil
		},
	})
	m.store = &openai.VectorStore{ID: "vs_demo", Name: "demo"}
	m.enter("home")
	m.Update(tea.PasteMsg{Content: query})
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.True(t, m.searched)
	assert.Equal(t, query, m.submittedQuery)
	assert.Contains(t, m.View().Content, "More matches exist")
	assert.Contains(t, m.View().Content, "kite.txt")
	modelKey(m, tea.KeyEnter)
	assert.Equal(t, "passage", m.page)
	assert.Contains(t, m.View().Content, "The kite has seven ribbons.")
	assert.Contains(t, m.View().Content, "The ribbons are blue.")
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "home", m.page)
	assert.Equal(t, query, m.editor.Value())
}

func TestWorkspaceUploadRetainsFileAndChecksBeforeRetry(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing attachment", true: "attachment succeeded remotely"}[exists], func(t *testing.T) {
			var calls []string
			m := modelForTest(t, modelService{
				upload: func(_ context.Context, path string) (*openai.FileObject, error) {
					calls = append(calls, "upload")
					assert.Equal(t, "/synthetic/kite.txt", path)
					return &openai.FileObject{ID: "file_demo", Filename: "kite.txt"}, nil
				},
				attach: func(_ context.Context, store, file string) (*openai.VectorStoreFile, error) {
					calls = append(calls, "attach")
					assert.Equal(t, "vs_demo", store)
					assert.Equal(t, "file_demo", file)
					if len(calls) == 2 {
						return nil, errors.New("synthetic attachment failure")
					}
					return &openai.VectorStoreFile{ID: file, VectorStoreID: store, Status: "completed"}, nil
				},
				getFile: func(_ context.Context, store, file string) (*openai.VectorStoreFile, error) {
					calls = append(calls, "check")
					assert.Equal(t, "vs_demo", store)
					assert.Equal(t, "file_demo", file)
					if !exists {
						return nil, &openai.Error{StatusCode: 404}
					}
					return &openai.VectorStoreFile{ID: file, VectorStoreID: store, Status: "completed"}, nil
				},
			})
			m.store = &openai.VectorStore{ID: "vs_demo"}
			m.enter("home")
			modelKey(m, tea.KeyTab)
			modelKey(m, tea.KeyTab)
			modelKey(m, tea.KeyEnter)
			require.Equal(t, "upload", m.page)
			m.Update(tea.PasteMsg{Content: "/synthetic/kite.txt"})
			require.Empty(t, calls)
			modelKey(m, tea.KeyEnter)
			cmd := modelKey(m, tea.KeyEnter)
			next := modelComplete(t, m, cmd)
			require.Equal(t, "file_demo", m.pendingFile)
			modelComplete(t, m, next)
			require.Equal(t, []string{"upload", "attach"}, calls)
			require.Equal(t, "file_demo", m.pendingFile)
			assert.Contains(t, m.View().Content, "file_demo")
			assert.Contains(t, m.View().Content, "Attach uploaded file")
			m.lastMutation = time.Time{}
			next = modelComplete(t, m, modelKey(m, tea.KeyEnter))
			if !exists {
				modelComplete(t, m, next)
				assert.Equal(t, []string{"upload", "attach", "check", "attach"}, calls)
			} else {
				assert.Equal(t, []string{"upload", "attach", "check"}, calls)
			}
			assert.Equal(t, "home", m.page)
			assert.Empty(t, m.pendingFile)
			assert.Nil(t, m.lastErr)
			assert.Contains(t, m.note, "indexed")
		})
	}
}

func TestWorkspaceCancelledMutationRetainsReturnedResource(t *testing.T) {
	t.Run("upload", func(t *testing.T) {
		m := modelForTest(t, modelService{
			upload: func(ctx context.Context, _ string) (*openai.FileObject, error) {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				return &openai.FileObject{ID: "file_late", Filename: "late.txt"}, nil
			},
		})
		m.store = &openai.VectorStore{ID: "vs_demo"}
		m.enter("upload")
		m.Update(tea.PasteMsg{Content: "/synthetic/late.txt"})
		modelKey(m, tea.KeyEnter)
		cmd := modelKey(m, tea.KeyEnter)
		modelKey(m, tea.KeyEscape)
		require.True(t, m.busy, "cancellation waits for the operation's result")
		require.True(t, m.cancelling)
		require.Nil(t, modelKey(m, tea.KeyEnter), "cancelled work cannot be replaced before completion")
		next := modelComplete(t, m, cmd)
		assert.Nil(t, next, "a cancelled upload must not attach automatically")
		assert.False(t, m.busy)
		assert.Equal(t, "file_late", m.pendingFile)
		assert.Contains(t, m.View().Content, "file_late")
	})
	t.Run("create", func(t *testing.T) {
		m := modelForTest(t, modelService{
			createStore: func(ctx context.Context, name string) (*openai.VectorStore, error) {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				return &openai.VectorStore{ID: "vs_late", Name: name}, nil
			},
		})
		m.enter("create")
		m.Update(tea.PasteMsg{Content: "late store"})
		modelKey(m, tea.KeyEnter)
		cmd := modelKey(m, tea.KeyEnter)
		modelKey(m, tea.KeyEscape)
		modelComplete(t, m, cmd)
		require.NotNil(t, m.store)
		assert.Equal(t, "vs_late", m.store.ID)
		assert.Equal(t, "vs_late", m.stores[0].ID)
		assert.False(t, m.busy)
	})
}

func TestWorkspaceUploadCleanupErrorRetainsIDAcrossStoreSwitch(t *testing.T) {
	m := modelForTest(t, modelService{
		upload: func(context.Context, string) (*openai.FileObject, error) {
			return &openai.FileObject{ID: "file_retained", Filename: "kite.txt"}, errors.New("synthetic cleanup failure")
		},
		getStore: func(_ context.Context, id string) (*openai.VectorStore, error) {
			return &openai.VectorStore{ID: id, Name: id}, nil
		},
		listFiles: func(_ context.Context, store, after string) (FilePage, error) {
			assert.Equal(t, "vs_second", store)
			assert.Empty(t, after)
			return FilePage{}, errors.New("synthetic file list failure")
		},
	})
	m.stores = []openai.VectorStore{{ID: "vs_first"}, {ID: "vs_second"}}
	m.store = &m.stores[0]
	m.files = []openai.VectorStoreFile{{ID: "file_first_store"}}
	m.fileNext = "file_first_store"
	m.enter("upload")
	m.Update(tea.PasteMsg{Content: "/synthetic/kite.txt"})
	modelKey(m, tea.KeyEnter)
	require.Nil(t, modelComplete(t, m, modelKey(m, tea.KeyEnter)))
	require.Equal(t, "file_retained", m.pendingFile)
	require.Error(t, m.lastErr)
	assert.Contains(t, m.note, "retained")

	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	require.Equal(t, "stores", m.page)
	modelKey(m, tea.KeyDown)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "vs_second", m.store.ID)
	assert.Empty(t, m.pendingFile, "a retained upload belongs to its original store")
	assert.Empty(t, m.files)
	assert.Empty(t, m.fileNext, "file cursors must not cross store boundaries")
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Empty(t, m.files)
	assert.Empty(t, m.fileNext, "a failed first load must not restore another store's cursor")
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "vs_first", m.store.ID)
	assert.Equal(t, "file_retained", m.pendingFile)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyEnter)
	assert.Contains(t, m.View().Content, "file_retained")
}

func TestWorkspaceSearchCancellationAndStaleResults(t *testing.T) {
	var requestCtx context.Context
	m := modelForTest(t, modelService{
		search: func(ctx context.Context, _, _ string) (SearchPage, error) {
			requestCtx = ctx
			return SearchPage{}, ctx.Err()
		},
	})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.enter("home")
	m.Update(tea.PasteMsg{Content: "question"})
	cmd := modelKey(m, tea.KeyEnter)
	oldID := m.seq
	modelKey(m, tea.KeyEscape)
	modelComplete(t, m, cmd)
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
	assert.False(t, m.quit)
	assert.False(t, m.busy)
	assert.Contains(t, m.note, "Stopped")

	m.service = modelService{search: func(context.Context, string, string) (SearchPage, error) {
		return SearchPage{Results: []openai.VectorStoreSearchResponse{{FileID: "file_current"}}}, nil
	}}
	cmd = modelKey(m, tea.KeyEnter)
	m.Update(workMessage{id: oldID, kind: "search", search: SearchPage{
		Results: []openai.VectorStoreSearchResponse{{FileID: "file_stale"}},
	}})
	require.True(t, m.busy)
	require.Empty(t, m.results)
	modelComplete(t, m, cmd)
	require.Len(t, m.results, 1)
	assert.Equal(t, "file_current", m.results[0].FileID)
}

func TestWorkspaceFailedRefreshPreservesPagination(t *testing.T) {
	t.Run("stores", func(t *testing.T) {
		calls := 0
		m := modelForTest(t, modelService{
			listStores: func(_ context.Context, after string) (StorePage, error) {
				calls++
				switch calls {
				case 1:
					require.Empty(t, after)
					return StorePage{Stores: []openai.VectorStore{{ID: "vs_first"}}, Next: "vs_first"}, nil
				case 2:
					require.Empty(t, after)
					return StorePage{}, errors.New("synthetic refresh failure")
				default:
					require.Equal(t, 3, calls)
					require.Equal(t, "vs_first", after)
					return StorePage{Stores: []openai.VectorStore{{ID: "vs_second"}}}, nil
				}
			},
		})
		modelComplete(t, m, m.loadStores(false))
		modelKey(m, tea.KeyTab)
		modelKey(m, tea.KeyTab)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		require.Len(t, m.stores, 1)
		assert.Equal(t, "vs_first", m.stores[0].ID)
		assert.Equal(t, "vs_first", m.storeNext)
		modelKey(m, tea.KeyTab)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		require.Len(t, m.stores, 2)
		assert.Equal(t, "vs_first", m.stores[0].ID)
		assert.Equal(t, "vs_second", m.stores[1].ID)
		assert.Empty(t, m.storeNext)
	})
	t.Run("files", func(t *testing.T) {
		calls := 0
		m := modelForTest(t, modelService{
			listFiles: func(_ context.Context, store, after string) (FilePage, error) {
				require.Equal(t, "vs_demo", store)
				calls++
				switch calls {
				case 1:
					require.Empty(t, after)
					return FilePage{Files: []openai.VectorStoreFile{{ID: "file_first"}}, Next: "file_first"}, nil
				case 2:
					require.Empty(t, after)
					return FilePage{}, errors.New("synthetic refresh failure")
				default:
					require.Equal(t, 3, calls)
					require.Equal(t, "file_first", after)
					return FilePage{Files: []openai.VectorStoreFile{{ID: "file_second"}}}, nil
				}
			},
		})
		m.store = &openai.VectorStore{ID: "vs_demo"}
		m.enter("home")
		modelKey(m, tea.KeyTab)
		modelKey(m, tea.KeyTab)
		modelKey(m, tea.KeyTab)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		modelKey(m, tea.KeyTab)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		require.Len(t, m.files, 1)
		assert.Equal(t, "file_first", m.files[0].ID)
		assert.Equal(t, "file_first", m.fileNext)
		modelKey(m, tea.KeyTab)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		require.Len(t, m.files, 2)
		assert.Equal(t, "file_first", m.files[0].ID)
		assert.Equal(t, "file_second", m.files[1].ID)
		assert.Empty(t, m.fileNext)
	})
}

func TestWorkspaceWatchingAnotherFilePreservesRetainedUpload(t *testing.T) {
	m := modelForTest(t, modelService{
		upload: func(context.Context, string) (*openai.FileObject, error) {
			return &openai.FileObject{ID: "file_retained", Filename: "retained.txt"}, nil
		},
		attach: func(context.Context, string, string) (*openai.VectorStoreFile, error) {
			return nil, errors.New("synthetic attachment failure")
		},
		listFiles: func(context.Context, string, string) (FilePage, error) {
			return FilePage{Files: []openai.VectorStoreFile{{
				ID: "file_other", VectorStoreID: "vs_demo", Status: "in_progress",
			}}}, nil
		},
		getFile: func(_ context.Context, store, file string) (*openai.VectorStoreFile, error) {
			assert.Equal(t, "vs_demo", store)
			assert.Equal(t, "file_other", file)
			return &openai.VectorStoreFile{ID: file, VectorStoreID: store, Status: "completed"}, nil
		},
	})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.enter("upload")
	m.Update(tea.PasteMsg{Content: "/synthetic/retained.txt"})
	modelKey(m, tea.KeyEnter)
	modelComplete(t, m, modelComplete(t, m, modelKey(m, tea.KeyEnter)))
	require.Equal(t, "file_retained", m.pendingFile)
	m.lastMutation = time.Time{}
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "files", m.page)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "file_retained", m.pendingFile)
	assert.False(t, m.pendingAttached)
	require.Len(t, m.files, 1)
	assert.Equal(t, "completed", string(m.files[0].Status))
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyEnter)
	assert.Contains(t, m.View().Content, "file_retained")
	assert.Contains(t, m.View().Content, "Attach uploaded file")
}

func TestWorkspaceBrowserUploadsExactSelectedPath(t *testing.T) {
	t.Run("directional mark", func(t *testing.T) {
		testBrowserUploadsSelectedPath(t, "kite\u2066blue.txt", `kite\u2066blue.txt`)
	})
	t.Run("tab", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows forbids tabs in filenames")
		}
		testBrowserUploadsSelectedPath(t, "kite\tblue.txt", `kite\tblue.txt`)
	})
}

func testBrowserUploadsSelectedPath(t *testing.T, selectedName, displayName string) {
	t.Helper()
	directory := t.TempDir()
	selectedPath := filepath.Join(directory, selectedName)
	siblingPath := filepath.Join(directory, "kite blue.txt")
	require.NoError(t, os.WriteFile(selectedPath, []byte("selected synthetic file"), 0600))
	require.NoError(t, os.WriteFile(siblingPath, []byte("different synthetic file"), 0600))
	var uploadedPath string
	m := modelForTest(t, modelService{
		upload: func(_ context.Context, path string) (*openai.FileObject, error) {
			uploadedPath = path
			return nil, errors.New("synthetic stop after checking the upload path")
		},
	})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.directory = directory
	m.enter("upload")
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "browse", m.page)
	require.Len(t, m.entries, 2)
	for index, entry := range m.entries {
		if entry.name == filepath.Base(selectedPath) {
			for range index {
				modelKey(m, tea.KeyDown)
			}
			break
		}
	}
	require.Equal(t, filepath.Base(selectedPath), m.entries[m.selected].name)
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "upload", m.page)
	require.Equal(t, filepath.Join(directory, displayName), m.editor.Value())
	require.Empty(t, uploadedPath, "selecting a file must not upload it")
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, selectedPath, uploadedPath)
	assert.NotEqual(t, siblingPath, uploadedPath)
}

func TestWorkspaceQAndPasteEditWithoutSubmission(t *testing.T) {
	for _, page := range []string{"create", "home", "upload"} {
		t.Run(page, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.store = &openai.VectorStore{ID: "vs_demo"}
			m.enter(page)
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			assert.False(t, m.quit)
			assert.Equal(t, "q", m.editor.Value())
			m.Update(tea.PasteMsg{Content: "uestion\r\nsecond line"})
			assert.Equal(t, "question second line", m.editor.Value())
			assert.False(t, m.busy)
			modelKey(m, tea.KeyTab)
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			assert.True(t, m.quit, "q exits when the text field does not have focus")
		})
	}
}

func TestWorkspaceSmallGeometryCannotSubmit(t *testing.T) {
	for _, page := range []string{"create", "home", "upload"} {
		for _, size := range []tea.WindowSizeMsg{{Width: 39, Height: 30}, {Width: 100, Height: 11}} {
			t.Run(fmt.Sprintf("%s/%dx%d", page, size.Width, size.Height), func(t *testing.T) {
				m := modelForTest(t, modelService{})
				m.store = &openai.VectorStore{ID: "vs_demo"}
				m.enter(page)
				m.editor.SetValue("synthetic input")
				if page == "create" {
					modelKey(m, tea.KeyTab)
				} else if page == "upload" {
					modelKey(m, tea.KeyTab)
					modelKey(m, tea.KeyTab)
				}
				m.Update(size)
				assert.Contains(t, m.View().Content, "Resize")
				assert.Nil(t, modelKey(m, tea.KeyEnter))
				assert.False(t, m.busy)
				m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				assert.True(t, m.quit)
				assert.Equal(t, 130, m.exitCode)
			})
		}
	}
}

func TestWorkspaceShutdownWaitsAndRejectsQueuedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m := newWorkspace(ctx, modelService{}, nil)
	started := make(chan struct{})
	finished := make(chan struct{})
	command := m.start("synthetic", "Working", func(ctx context.Context) workMessage {
		close(started)
		<-ctx.Done()
		close(finished)
		return workMessage{err: ctx.Err()}
	})
	queued := m.workers.command(ctx, func(context.Context) workMessage {
		t.Error("a command queued before shutdown must not start afterward")
		return workMessage{}
	})
	go command()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not start")
	}
	done := make(chan struct{})
	go func() {
		m.shutdown(cancel)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("shutdown did not stop its worker")
	}
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before the worker finished")
	}
	assert.Nil(t, queued())
	assert.Nil(t, m.workers.command(ctx, func(context.Context) workMessage {
		t.Error("shutdown must reject newly queued work")
		return workMessage{}
	})())
}

func TestWorkspaceEscapesRemoteControls(t *testing.T) {
	control := "synthetic\x1b]52;c;Y2xpcGJvYXJk\a\r\x1b[2J"
	for _, page := range []string{"stores", "home", "files", "browse", "passage", "upload"} {
		t.Run(page, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.store = &openai.VectorStore{ID: "vs_demo", Name: control}
			m.stores = []openai.VectorStore{*m.store}
			m.results = []openai.VectorStoreSearchResponse{{
				FileID: "file_demo", Filename: control,
				Content: []openai.VectorStoreSearchResponseContent{{Type: "text", Text: control}},
			}}
			m.files = []openai.VectorStoreFile{{ID: "file_demo", Status: "completed"}}
			m.names["file_demo"] = control
			m.entries = []browserEntry{{name: control}}
			m.directory, m.passage, m.note = control, control, control
			m.pendingFile, m.searched = control, true
			m.enter(page)
			view := m.View().Content
			assert.Contains(t, view, "synthetic")
			assert.NotContains(t, view, "\x1b]52;")
			assert.NotContains(t, view, "\x1b[2J")
			assert.NotContains(t, view, "\a")
			assert.NotContains(t, view, "\r")
			for _, line := range strings.Split(view, "\n") {
				assert.LessOrEqual(t, ansi.StringWidth(line), m.width)
			}
		})
	}
}
