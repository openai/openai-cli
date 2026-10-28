package vectorstores

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
)

type workspaceWorkers struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func (w *workspaceWorkers) command(ctx context.Context, run func(context.Context) workMessage) tea.Cmd {
	return func() tea.Msg {
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil
		}
		w.wg.Add(1)
		w.mu.Unlock()
		defer w.wg.Done()
		return run(ctx)
	}
}

type browserEntry struct {
	name      string
	directory bool
}
type retainedUpload struct {
	file, name string
	attached   bool
	options    AttachOptions
}
type workMessage struct {
	managementResult
	reset     bool
	id        uint64
	kind      string
	err       error
	stores    StorePage
	files     FilePage
	search    SearchPage
	store     *openai.VectorStore
	file      *openai.FileObject
	attached  *openai.VectorStoreFile
	entries   []browserEntry
	directory string
}

// The search field and five store actions precede the match list in tab order.
const matchListFocus = 6

type workspace struct {
	managementState
	ctx                                           context.Context
	service                                       Service
	errorMessage                                  func(error) string
	workers                                       workspaceWorkers
	width, height                                 int
	color, dark                                   bool
	exitCode                                      int
	lastErr                                       error
	quit                                          bool
	page                                          string
	focus, selected, resultSelected, offset       int
	editor                                        textarea.Model
	query, submittedQuery, uploadPath, createName string
	selectedUploadPath                            string
	stores                                        []openai.VectorStore
	storesLoaded                                  bool
	store                                         *openai.VectorStore
	storeNext, fileNext                           string
	files                                         []openai.VectorStoreFile
	results                                       []openai.VectorStoreSearchResponse
	moreResults, searched                         bool
	pendingFile, pendingName                      string
	pendingAttached                               bool
	retainedUploads                               map[string]retainedUpload
	names                                         map[string]string
	directory                                     string
	entries                                       []browserEntry
	passage                                       string
	passageLines                                  []string
	note                                          string
	busy, cancelling                              bool
	stage                                         string
	seq                                           uint64
	cancel                                        context.CancelFunc
	lastMutation                                  time.Time
	retryNeedsInput                               bool
}

func newWorkspace(ctx context.Context, service Service, errorMessage func(error) string) *workspace {
	editor := textarea.New()
	editor.CharLimit = 0
	editor.ShowLineNumbers = false
	editor.Prompt = ""
	editor.SetWidth(60)
	editor.SetHeight(1)
	editor.KeyMap.Paste.SetEnabled(false)
	editor.KeyMap.CopySelection.SetEnabled(false)
	directory, err := os.Getwd()
	if err != nil {
		directory = "."
	}
	if errorMessage == nil {
		errorMessage = func(error) string { return "The request failed. Check your connection and API configuration." }
	}
	return &workspace{ctx: ctx, service: service, errorMessage: errorMessage, page: "stores",
		editor: editor, color: os.Getenv("NO_COLOR") == "", dark: true,
		names: make(map[string]string), retainedUploads: make(map[string]retainedUpload), directory: directory}
}

func (m *workspace) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.loadStores(false))
}

func (m *workspace) shutdown(cancel context.CancelFunc) {
	m.workers.mu.Lock()
	m.workers.closed = true
	cancel()
	if m.cancel != nil {
		m.cancel()
	}
	m.workers.mu.Unlock()
	m.workers.wg.Wait()
}

func (m *workspace) start(kind, stage string, run func(context.Context) workMessage) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.seq++
	id := m.seq
	m.busy, m.cancelling, m.stage = true, false, stage
	m.note = ""
	return m.workers.command(ctx, func(ctx context.Context) workMessage {
		msg := run(ctx)
		msg.id, msg.kind = id, kind
		return msg
	})
}

func (m *workspace) loadStores(more bool) tea.Cmd {
	cursor := ""
	if more {
		cursor = m.storeNext
	}
	return m.start("stores", "Loading stores…", func(ctx context.Context) workMessage {
		page, err := m.service.ListStores(ctx, cursor)
		return workMessage{stores: page, err: err, reset: !more}
	})
}

func (m *workspace) loadFiles(more bool) tea.Cmd {
	cursor, id := "", m.store.ID
	batch := ""
	if m.page == "batchfiles" && m.batch != nil {
		batch = m.batch.ID
	}
	if more {
		cursor = m.fileNext
	}
	return m.start("files", "Loading files…", func(ctx context.Context) workMessage {
		if batch != "" {
			page, err := m.manager().ListBatchFiles(ctx, id, batch, cursor)
			return workMessage{files: page, err: err, reset: !more}
		}
		page, err := m.service.ListFiles(ctx, id, cursor)
		return workMessage{files: page, err: err, reset: !more}
	})
}

func (m *workspace) enter(page string) tea.Cmd {
	m.saveInput()
	m.note = ""
	if m.store != nil {
		m.retainedUploads[m.store.ID] = retainedUpload{m.pendingFile, m.pendingName, m.pendingAttached, m.attachOptions}
	}
	m.page, m.focus, m.selected, m.offset = page, 0, 0, 0
	if page == "stores" && m.listLength() == 0 {
		m.focus = 1
	}
	switch page {
	case "home":
		m.editor.SetValue(m.query)
		m.editor.Placeholder = "Type a question"
	case "create":
		m.editor.SetValue(m.createName)
		m.editor.Placeholder = "Name your vector store"
	case "upload":
		m.editor.SetValue(m.uploadPath)
		if m.selectedUploadPath != "" {
			m.editor.SetValue(cleanLine(m.selectedUploadPath))
		}
		m.editor.Placeholder = "Paste a file path, or choose Browse files"
	default:
		m.managementInput(page)
	}
	return m.syncFocus()
}

func (m *workspace) saveInput() {
	switch m.page {
	case "home":
		m.query = m.editor.Value()
	case "create":
		m.createName = m.editor.Value()
	case "upload":
		if m.selectedUploadPath == "" {
			m.uploadPath = m.editor.Value()
		}
	}
}

func (m *workspace) editing() bool {
	if m.page == "upload" && m.pendingFile != "" {
		return false
	}
	_, form := managementForms[m.page]
	return m.focus == 0 && (m.page == "home" || m.page == "create" || m.page == "upload" || form)
}

func (m *workspace) syncFocus() tea.Cmd {
	if m.editing() {
		cmd := m.editor.Focus()
		m.layoutEditor()
		return cmd
	}
	m.editor.Blur()
	m.layoutEditor()
	return nil
}

func (m *workspace) focusCount() int {
	if _, form := managementForms[m.page]; form {
		return 3
	}
	switch m.page {
	case "files", "batchfiles":
		return 4
	case "stores", "upload", "sources":
		return 5
	case "home":
		if len(m.results) > 0 {
			return 7
		}
		return 6
	case "create", "browse":
		return 3
	default:
		return 1
	}
}

func (m *workspace) listLength() int {
	if menu := managementMenus[m.page]; menu != nil {
		return len(menu)
	}
	switch m.page {
	case "stores":
		return len(m.filteredStores())
	case "sources":
		return len(m.sourceFiles)
	case "files", "batchfiles":
		return len(m.files)
	case "browse":
		return len(m.entries)
	case "home":
		return len(m.results)
	}
	return 0
}

func (m *workspace) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quit {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutEditor()
		if m.page == "passage" || m.page == "document" {
			m.layoutPassage()
		}
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		if m.page == "document" && len(m.documentFields) > 0 {
			m.layoutPassage()
		}
		return m, nil
	case workMessage:
		if msg.id != m.seq {
			return m, nil
		}
		return m, m.complete(msg)
	case tea.KeyPressMsg:
		key := msg.String()
		// Only suppress repeated Enter after a mutation. Deliberate navigation or
		// editing must remain responsive immediately after a successful request.
		if key != "enter" {
			m.lastMutation = time.Time{}
			if m.retryNeedsInput {
				page, focus := m.page, m.focus
				defer func() {
					if m.page != page || m.focus != focus {
						m.retryNeedsInput = false
					}
				}()
			}
		}
		if key == "ctrl+c" {
			m.quit, m.exitCode = true, 130
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		if m.busy {
			if key == "esc" {
				m.cancelling = true
				m.cancel()
				m.stage = "Stopping local work…"
			}
			return m, nil
		}
		if key == "q" && !m.editing() {
			m.quit = true
			return m, tea.Quit
		}
		if key == "esc" {
			return m, m.back()
		}
		if m.width < 40 || m.height < 12 {
			return m, nil
		}
		switch key {
		case "tab", "shift+tab":
			delta := 1
			if key == "shift+tab" {
				delta = -1
			}
			m.focus = (m.focus + delta + m.focusCount()) % m.focusCount()
			if m.page == "stores" && m.focus == 3 && m.storeNext == "" {
				m.focus += delta
			}
			return m, m.syncFocus()
		case "enter":
			if m.retryNeedsInput || !m.lastMutation.IsZero() && time.Since(m.lastMutation) < 750*time.Millisecond {
				return m, nil
			}
			return m, m.activate()
		case "home", "end":
			if m.page == "passage" || m.page == "document" {
				m.offset = 0
				if key == "end" {
					m.offset = max(0, len(m.passageLines)-m.passageRows())
				}
				return m, nil
			}
		case "up", "down", "pgup", "pgdown":
			if key == "up" || key == "down" {
				if cmd, handled := m.navigate(key); handled {
					return m, cmd
				}
			}
			if !m.editing() {
				delta := 1
				if key == "up" || key == "pgup" {
					delta = -1
				}
				if key == "pgup" || key == "pgdown" {
					delta *= max(1, m.height-9)
				}
				if m.page == "passage" || m.page == "document" {
					m.offset = max(0, min(max(0, len(m.passageLines)-m.passageRows()), m.offset+delta))
				} else if m.focus == 0 || m.page == "home" && m.focus == matchListFocus {
					if m.page == "home" {
						m.resultSelected = max(0, min(m.listLength()-1, m.resultSelected+delta))
					} else {
						m.selected = max(0, min(m.listLength()-1, m.selected+delta))
					}
				}
				return m, nil
			}
		case "left", "right":
			if cmd, handled := m.navigate(key); handled {
				return m, cmd
			}
		}
	}
	if m.editing() && !m.busy {
		if paste, ok := message.(tea.PasteMsg); ok {
			m.lastMutation = time.Time{}
			paste.Content = strings.ReplaceAll(strings.ReplaceAll(paste.Content, "\r", ""), "\n", " ")
			message = paste
		}
		var cmd tea.Cmd
		previous := m.editor.Value()
		m.editor, cmd = m.editor.Update(message)
		if m.editor.Value() != previous {
			m.retryNeedsInput = false
			if m.page == "upload" {
				m.selectedUploadPath = ""
			}
		}
		m.saveInput()
		return m, cmd
	}
	return m, nil
}

func (m *workspace) back() tea.Cmd {
	switch m.page {
	case "passage":
		m.page, m.focus, m.offset = "home", matchListFocus, 0
		return m.syncFocus()
	case "document":
		return m.enter(m.documentBack)
	case "filter":
		return m.enter("stores")
	case "rename", "expiration", "metadata", "open-batch", "delete-store", "sources":
		return m.enter("manage")
	case "attributes", "remove-file":
		return m.enter("file")
	case "file":
		return m.enter(m.fileOrigin)
	case "batchfiles", "cancel-batch":
		return m.enter("batch")
	case "batch":
		return m.enter("manage")
	case "attach-options":
		return m.enter(m.optionsBack)
	case "stores":
		m.quit = true
		return tea.Quit
	case "browse":
		return m.enter("upload")
	case "home":
		if m.focus == matchListFocus {
			m.focus = 0
			return m.syncFocus()
		}
		return m.enter("stores")
	case "create":
		return m.enter("stores")
	default:
		return m.enter("home")
	}
}

func (m *workspace) activate() tea.Cmd {
	if cmd, handled := m.activateManagement(); handled {
		return cmd
	}
	switch m.page {
	case "stores":
		switch m.focus {
		case 0:
			stores := m.filteredStores()
			if len(stores) == 0 {
				return nil
			}
			id := stores[m.selected].ID
			return m.start("store", "Opening store…", func(ctx context.Context) workMessage {
				store, err := m.service.GetStore(ctx, id)
				return workMessage{store: store, err: err}
			})
		case 1:
			return m.enter("create")
		case 2:
			return m.loadStores(false)
		case 3:
			if m.storeNext != "" {
				return m.loadStores(true)
			}
		}
	case "create":
		if m.focus == 0 {
			m.focus = 1
			return m.syncFocus()
		}
		if m.focus == 2 {
			return m.back()
		}
		name := strings.TrimSpace(m.editor.Value())
		if name == "" {
			m.note = "Enter a store name."
			m.focus = 0
			return m.syncFocus()
		}
		return m.start("create", "Creating store…", func(ctx context.Context) workMessage {
			store, err := m.service.CreateStore(ctx, name)
			return workMessage{store: store, err: err}
		})
	case "home":
		switch m.focus {
		case 0, 1:
			query, id := m.editor.Value(), m.store.ID
			if strings.TrimSpace(query) == "" {
				m.note = "Enter a search question."
				return nil
			}
			m.query, m.submittedQuery = query, query
			m.results, m.searched, m.moreResults, m.resultSelected = nil, false, false, 0
			return m.start("search", "Searching files…", func(ctx context.Context) workMessage {
				result, err := m.service.Search(ctx, id, query)
				return workMessage{search: result, err: err}
			})
		case 2:
			if !m.mutable() {
				return nil
			}
			return m.enter("upload")
		case 3:
			m.files, m.fileNext = nil, ""
			m.enter("files")
			return m.loadFiles(false)
		case 4:
			return m.enter("stores")
		case matchListFocus:
			result := m.results[m.resultSelected]
			var parts []string
			for _, content := range result.Content {
				parts = append(parts, content.Text)
			}
			m.passage = strings.Join(parts, "\n\n")
			m.layoutPassage()
			return m.enter("passage")
		}
	case "upload":
		switch m.focus {
		case 0:
			m.focus = 2
			return m.syncFocus()
		case 1:
			if m.pendingFile != "" {
				m.note = "Source file " + m.pendingFile + " remains in Files storage."
				m.pendingFile, m.pendingName, m.uploadPath = "", "", ""
				m.selectedUploadPath = ""
				m.pendingAttached = false
				m.attachOptions = AttachOptions{}
				m.editor.SetValue("")
				return nil
			}
			m.enter("browse")
			return m.readDirectory(m.directory)
		case 2:
			if !m.mutable() {
				return nil
			}
			if m.pendingFile != "" {
				return m.checkAttachment()
			}
			path := m.editor.Value()
			if m.selectedUploadPath != "" {
				path = m.selectedUploadPath
			}
			if strings.TrimSpace(path) == "" {
				m.note = "Choose a file first."
				return nil
			}
			if strings.HasPrefix(path, "~/") {
				home, err := os.UserHomeDir()
				if err != nil {
					m.fail(errors.New("home directory unavailable"))
					return nil
				}
				path = filepath.Join(home, path[2:])
			}
			m.uploadPath = m.editor.Value()
			m.pendingName = filepath.Base(path)
			return m.start("upload", "Uploading file…", func(ctx context.Context) workMessage {
				file, err := m.service.UploadFile(ctx, path)
				return workMessage{file: file, err: err}
			})
		case 3:
			return m.back()
		}
	case "files", "batchfiles":
		switch m.focus {
		case 0:
			if len(m.files) == 0 {
				return nil
			}
			file := m.files[m.selected]
			m.note = "File " + file.ID + ": " + string(file.Status)
			if file.Status == "in_progress" {
				return m.watchFile(file.ID, false)
			}
			m.fileOrigin = m.page
			id := m.store.ID
			return m.start("file-detail", "Reading file details…", func(ctx context.Context) workMessage {
				result, err := m.service.GetFile(ctx, id, file.ID)
				return workMessage{attached: result, err: err}
			})
		case 1:
			return m.loadFiles(false)
		case 2:
			if m.fileNext != "" {
				return m.loadFiles(true)
			}
		case 3:
			return m.back()
		}
	case "browse":
		switch m.focus {
		case 0:
			if len(m.entries) == 0 {
				return nil
			}
			entry := m.entries[m.selected]
			path := filepath.Join(m.directory, entry.name)
			if entry.directory {
				return m.readDirectory(path)
			}
			m.uploadPath = path
			m.selectedUploadPath = path
			m.enter("upload")
			m.focus = 2
			return m.syncFocus()
		case 1:
			return m.readDirectory(filepath.Dir(m.directory))
		case 2:
			return m.back()
		}
	}
	return nil
}

func (m *workspace) readDirectory(path string) tea.Cmd {
	return m.start("browse", "Reading folder…", func(ctx context.Context) workMessage {
		entries, err := os.ReadDir(path)
		if err != nil {
			return workMessage{err: workspaceFailure("Could not read this folder. Choose another folder or enter a file path.", err)}
		}
		result := make([]browserEntry, 0, len(entries))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return workMessage{err: err}
			}
			directory := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := os.Stat(filepath.Join(path, entry.Name()))
				if err != nil {
					continue
				}
				directory = info.IsDir()
			}
			result = append(result, browserEntry{entry.Name(), directory})
		}
		sort.SliceStable(result, func(i, j int) bool {
			if result[i].directory != result[j].directory {
				return result[i].directory
			}
			return result[i].name < result[j].name
		})
		return workMessage{entries: result, directory: path}
	})
}

func (m *workspace) attach() tea.Cmd {
	id, file := m.store.ID, m.pendingFile
	opts := m.attachOptions
	return m.start("attach", "Attaching file…", func(ctx context.Context) workMessage {
		if opts.Attributes != nil || opts.Chunking != nil {
			result, err := m.manager().AttachConfigured(ctx, id, file, opts)
			return workMessage{attached: result, err: err}
		}
		result, err := m.service.AttachFile(ctx, id, file)
		return workMessage{attached: result, err: err}
	})
}

// A manual retry checks remote state before sending another attachment request.
func (m *workspace) checkAttachment() tea.Cmd {
	id, file := m.store.ID, m.pendingFile
	return m.start("check", "Checking attachment…", func(ctx context.Context) workMessage {
		result, err := m.service.GetFile(ctx, id, file)
		var apierr *openai.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			return workMessage{}
		}
		return workMessage{attached: result, err: err}
	})
}

func (m *workspace) poll(delay bool) tea.Cmd {
	return m.pollFile(m.pendingFile, delay, "index")
}

func (m *workspace) watchFile(file string, delay bool) tea.Cmd {
	return m.pollFile(file, delay, "watch")
}

func (m *workspace) pollFile(file string, delay bool, kind string) tea.Cmd {
	id := m.store.ID
	return m.start(kind, "Indexing file…", func(ctx context.Context) workMessage {
		if delay {
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return workMessage{err: ctx.Err()}
			case <-timer.C:
			}
		}
		result, err := m.service.GetFile(ctx, id, file)
		return workMessage{attached: result, err: err}
	})
}

func (m *workspace) fail(err error) {
	m.lastErr = err
	m.note = m.errorMessage(err)
}

// Keep the list in sync with store details returned by the API.
func (m *workspace) setStore(store *openai.VectorStore) {
	m.store = store
	for i := range m.stores {
		if m.stores[i].ID == store.ID {
			m.stores[i] = *store
			return
		}
	}
}

func (m *workspace) complete(msg workMessage) tea.Cmd {
	cancelled := m.cancelling
	mutation := msg.kind == "create" || msg.kind == "upload" || msg.kind == "attach" || managementMutation(msg.kind)
	if mutation {
		// An unsuccessful response can still mean that the server applied the write.
		m.lastMutation = time.Now()
	}
	m.busy, m.cancelling = false, false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if msg.kind == "upload" && msg.file != nil && msg.file.ID != "" {
		m.pendingFile = msg.file.ID
		m.names[msg.file.ID] = msg.file.Filename
		m.pendingAttached = false
	}
	if msg.err != nil {
		if cancelled || errors.Is(msg.err, context.Canceled) {
			m.note = "Stopped local work."
			if mutation && msg.kind != "create" && msg.kind != "upload" {
				m.note += " The server may have completed the request. Check before retrying."
			}
			if msg.kind == "index" || msg.kind == "watch" {
				m.note += " Remote indexing continues."
			}
		} else {
			m.fail(msg.err)
			if managementMutation(msg.kind) {
				m.note += " Check remote state before retrying."
			}
			if msg.kind == "attach" {
				m.note += " Uploaded file retained. Retry attachment below."
			}
		}
		var uncertain *unconfirmedMutation
		switch {
		case msg.kind == "upload" && m.pendingFile != "":
			m.note = "Upload completed; its file ID is retained. " + m.note
		case errors.As(msg.err, &uncertain):
			m.retryNeedsInput = true
			if msg.kind == "create" {
				m.note = "Refresh stores before retrying. The store may already exist. " + m.note
			} else {
				m.note = "Check project Files before retrying. The file may already exist. " + m.note
			}
		}
		return nil
	}
	m.lastErr = nil
	if cancelled && !mutation {
		m.note = "Stopped local work."
		switch msg.kind {
		case "watch":
			m.note = "Stopped waiting. Remote indexing continues."
		case "check", "index":
			m.note = "Stopped waiting. Remote indexing may continue."
		}
		return nil
	}
	if cmd, handled := m.completeManagement(msg); handled {
		return cmd
	}
	switch msg.kind {
	case "watch":
		if msg.attached == nil {
			m.fail(workspaceFailure("The API returned no file status. Refresh Files to try again.", nil))
			return nil
		}
		if msg.attached.Status == "in_progress" {
			return m.watchFile(msg.attached.ID, true)
		}
		for i := range m.files {
			if m.files[i].ID == msg.attached.ID {
				m.files[i] = *msg.attached
			}
		}
		m.note = "File " + msg.attached.ID + ": " + string(msg.attached.Status)
		if msg.attached.Status != "completed" {
			m.lastErr = workspaceFailure("File indexing did not complete. Check the file status before searching.", nil)
			if msg.attached.Status != "failed" && msg.attached.Status != "cancelled" {
				m.note = "The API returned an unrecognized file indexing status."
			}
		}
		return nil
	case "upload":
		if msg.file == nil || msg.file.ID == "" {
			m.fail(workspaceFailure("The upload returned no file ID. Check Files storage before retrying.", nil))
			return nil
		}
		if cancelled {
			m.note = "Upload completed. File retained; select Attach uploaded file to continue."
			return nil
		}
		return m.attach()
	case "check", "attach", "index":
		if cancelled {
			m.note = "Stopped waiting. Remote indexing may continue."
			return nil
		}
		if msg.kind == "check" && msg.attached == nil {
			return m.attach()
		}
		if msg.attached == nil {
			m.fail(workspaceFailure("The API returned no attachment. Check Files before retrying.", nil))
			return nil
		}
		m.pendingAttached = true
		switch msg.attached.Status {
		case "in_progress":
			return m.poll(true)
		case "completed":
			m.pendingFile, m.pendingName = "", ""
			m.pendingAttached = false
			m.attachOptions = AttachOptions{}
			m.lastMutation = time.Now()
			return m.enterWithNote("home", "File indexed. You can search it now.")
		case "failed", "cancelled":
			m.lastErr = errors.New("file indexing did not complete")
			m.note = "Indexing " + string(msg.attached.Status) + ". Check the file and choose another upload."
			m.pendingFile, m.pendingName = "", ""
			m.pendingAttached = false
			m.attachOptions = AttachOptions{}
		default:
			m.fail(errors.New("unrecognized file indexing status"))
		}
		return nil
	case "create", "store":
		if msg.store == nil || msg.store.ID == "" {
			m.fail(workspaceFailure("The API returned no store ID. Refresh stores before retrying.", nil))
			return nil
		}
		if m.store == nil || m.store.ID != msg.store.ID {
			if m.store != nil {
				m.retainedUploads[m.store.ID] = retainedUpload{m.pendingFile, m.pendingName, m.pendingAttached, m.attachOptions}
			}
			filter := m.storeFilter
			m.managementState = managementState{storeFilter: filter}
			m.results, m.files = nil, nil
			m.fileNext = ""
			m.query, m.submittedQuery = "", ""
			retained := m.retainedUploads[msg.store.ID]
			m.pendingFile, m.pendingName, m.pendingAttached = retained.file, retained.name, retained.attached
			m.attachOptions = retained.options
			m.searched, m.moreResults = false, false
		}
		m.setStore(msg.store)
		if msg.kind == "create" {
			m.stores = append([]openai.VectorStore{*msg.store}, m.stores...)
			m.createName = ""
		}
		// Avoid saving the previous screen's draft over a freshly selected store.
		m.page = ""
		if msg.kind == "create" {
			return m.enterWithNote("home", "Store created.")
		}
		return m.enter("home")
	}
	switch msg.kind {
	case "stores":
		m.storesLoaded = true
		if msg.reset {
			m.stores = nil
			m.selected = 0
		}
		m.stores = append(m.stores, msg.stores.Stores...)
		m.storeNext = msg.stores.Next
		if m.focus == 3 && m.storeNext == "" {
			m.focus = 0
		}
		if m.listLength() == 0 && m.focus == 0 {
			m.focus = 1
		}
	case "files":
		if msg.reset {
			m.files = nil
			m.selected = 0
		}
		m.files = append(m.files, msg.files.Files...)
		m.fileNext = msg.files.Next
	case "search":
		m.results, m.moreResults, m.searched = msg.search.Results, msg.search.More, true
		for _, result := range m.results {
			m.names[result.FileID] = result.Filename
		}
		m.focus = 0
		if len(m.results) > 0 {
			m.focus = matchListFocus
		}
		return m.syncFocus()
	case "browse":
		m.entries, m.directory, m.selected = msg.entries, msg.directory, 0
	}
	return nil
}
