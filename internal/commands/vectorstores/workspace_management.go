package vectorstores

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
)

type managementState struct {
	storeFilter              string
	file                     *openai.VectorStoreFile
	batch                    *openai.VectorStoreFileBatch
	fileOrigin, documentBack string
	documentTitle            string
	documentFields           []detailField
	sourceFiles              []openai.FileObject
	sourceNext               string
	selectedSources          map[string]bool
	attachOptions            AttachOptions
	batchOptions             AttachOptions
	optionsBack              string
}

type managementResult struct {
	batch   *openai.VectorStoreFileBatch
	sources SourcePage
	text    string
}

var managementMenus = map[string][]string{
	"manage": {"Store details", "Rename store", "Expiration", "Edit metadata", "Attach existing files", "Open file batch", "Delete store", "Back"},
	"file":   {"File details", "Read content", "Edit attributes", "Remove attachment", "Back"},
	"batch":  {"Batch details", "Batch files", "Refresh status", "Cancel processing", "Back"},
}

var managementForms = map[string]string{
	"filter": "Search stores by name or ID", "rename": "Store name", "expiration": "Days since last activity, or never",
	"metadata": "Metadata (JSON object of strings)", "attributes": "Attributes (JSON object)",
	"delete-store": "Confirm store ID", "remove-file": "Confirm file ID",
	"open-batch": "Batch ID", "cancel-batch": "Confirm batch ID", "attach-options": "Attachment options (JSON object)",
}

func (m *workspace) manager() ManagementService {
	service, _ := m.service.(ManagementService)
	return service
}

func (m *workspace) filteredStores() []openai.VectorStore {
	query := strings.ToLower(m.storeFilter)
	if query == "" {
		return m.stores
	}
	var result []openai.VectorStore
	for _, store := range m.stores {
		if strings.Contains(strings.ToLower(store.Name), query) || strings.Contains(strings.ToLower(store.ID), query) {
			result = append(result, store)
		}
	}
	return result
}

func (m *workspace) managementInput(page string) {
	value := ""
	switch page {
	case "filter":
		value = m.storeFilter
	case "rename":
		value = m.store.Name
	case "expiration":
		value = "never"
		if m.store.ExpiresAfter.Days > 0 {
			value = strconv.FormatInt(m.store.ExpiresAfter.Days, 10)
		}
	case "metadata":
		value = compactJSON(m.store.Metadata)
	case "attributes":
		value = compactJSON(m.file.Attributes)
	case "open-batch":
		if m.batch != nil {
			value = m.batch.ID
		}
	case "attach-options":
		value = compactJSON(*m.editableAttachOptions())
	}
	if _, ok := managementForms[page]; ok {
		if (page == "metadata" || page == "attributes") && value == "null" {
			value = "{}"
		}
		m.editor.SetValue(value)
		m.editor.Placeholder = map[string]string{
			"filter": "Store name or ID", "rename": "Name your vector store", "expiration": "Days, or never",
			"metadata": "{}", "attributes": "{}", "attach-options": "{}",
			"delete-store": "vs_…", "remove-file": "file-…", "open-batch": "vsfb_…", "cancel-batch": "vsfb_…",
		}[page]
	}
}

func (m *workspace) editableAttachOptions() *AttachOptions {
	if m.optionsBack == "sources" {
		return &m.batchOptions
	}
	return &m.attachOptions
}

func compactJSON(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func decodePairs(text string, attributes bool) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil || values == nil {
		return nil, fmt.Errorf("Enter a JSON object.")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("Enter one JSON object.")
	}
	if len(values) > 16 {
		return nil, fmt.Errorf("Use at most 16 entries.")
	}
	for key, value := range values {
		if utf8.RuneCountInString(key) > 64 {
			return nil, fmt.Errorf("Each key must contain at most 64 characters.")
		}
		switch value := value.(type) {
		case string:
			if utf8.RuneCountInString(value) > 512 {
				return nil, fmt.Errorf("Each string must contain at most 512 characters.")
			}
		case bool, json.Number:
			if attributes {
				continue
			}
			return nil, fmt.Errorf("Use strings as values.")
		default:
			return nil, fmt.Errorf("Use strings%s as values.", map[bool]string{true: ", numbers, or booleans"}[attributes])
		}
	}
	return values, nil
}

func parseAttachOptions(text string) (AttachOptions, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var result AttachOptions
	if strings.TrimSpace(text) == "null" || decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return result, fmt.Errorf("Use an object containing attributes and/or chunking_strategy.")
	}
	if result.Attributes != nil {
		if _, err := decodePairs(compactJSON(result.Attributes), true); err != nil {
			return result, err
		}
	}
	if c := result.Chunking; c != nil {
		kind, _ := c["type"].(string)
		if kind == "auto" && len(c) == 1 {
			return result, nil
		}
		static, _ := c["static"].(map[string]any)
		sizeNumber, _ := static["max_chunk_size_tokens"].(json.Number)
		overlapNumber, _ := static["chunk_overlap_tokens"].(json.Number)
		size, sizeErr := sizeNumber.Int64()
		overlap, overlapErr := overlapNumber.Int64()
		if kind != "static" || len(c) != 2 || len(static) != 2 || sizeErr != nil || overlapErr != nil || size < 100 || size > 4096 || overlap < 0 || overlap > size/2 {
			return result, fmt.Errorf("Static chunk size must be 100–4096 tokens; overlap must be 0 to half the size.")
		}
	}
	return result, nil
}

func (m *workspace) mutable() bool {
	if m.store != nil && m.store.Status == "expired" {
		m.note = "Expired stores are read-only. Create another store to add files."
		return false
	}
	return true
}

func (m *workspace) openDocument(title, text, back string) tea.Cmd {
	m.documentFields = nil
	m.documentTitle, m.documentBack, m.passage = title, back, text
	m.layoutPassage()
	return m.enter("document")
}

func (m *workspace) loadSources(more bool) tea.Cmd {
	after := ""
	if more {
		after = m.sourceNext
	}
	return m.start("sources", "Loading uploaded files…", func(ctx context.Context) workMessage {
		page, err := m.manager().ListSourceFiles(ctx, after)
		return workMessage{managementResult: managementResult{sources: page}, reset: !more, err: err}
	})
}

func (m *workspace) activateManagement() (tea.Cmd, bool) {
	if m.page == "home" && m.focus == 5 {
		id := m.store.ID
		return m.start("manage-store", "Refreshing store details…", func(ctx context.Context) workMessage {
			store, err := m.service.GetStore(ctx, id)
			return workMessage{store: store, err: err}
		}), true
	}
	if m.page == "stores" && m.focus == 4 {
		return m.enter("filter"), true
	}
	if menu := managementMenus[m.page]; menu != nil {
		if m.manager() == nil {
			m.note = "Management is unavailable with this API client."
			return nil, true
		}
		return m.activateManagementMenu(menu[m.selected]), true
	}
	if _, ok := managementForms[m.page]; ok {
		if m.focus == 0 {
			m.focus = 1
			return m.syncFocus(), true
		}
		if m.focus == 2 {
			return m.back(), true
		}
		return m.submitManagement(), true
	}
	if m.page == "sources" {
		switch m.focus {
		case 0:
			if len(m.sourceFiles) > 0 {
				id := m.sourceFiles[m.selected].ID
				m.selectedSources[id] = !m.selectedSources[id]
			}
		case 1:
			var ids []string
			seen := map[string]bool{}
			for _, file := range m.sourceFiles {
				if m.selectedSources[file.ID] && !seen[file.ID] {
					ids = append(ids, file.ID)
					seen[file.ID] = true
				}
			}
			if len(ids) == 0 {
				m.note = "Select files with Enter first."
				return nil, true
			}
			if !m.mutable() {
				return nil, true
			}
			id, opts := m.store.ID, m.batchOptions
			return m.start("batch-create", "Attaching selected files…", func(ctx context.Context) workMessage {
				batch, err := m.manager().CreateBatch(ctx, id, ids, opts)
				return workMessage{managementResult: managementResult{batch: batch}, err: err}
			}), true
		case 2:
			if m.sourceNext != "" {
				return m.loadSources(true), true
			}
		case 3:
			m.optionsBack = "sources"
			return m.enter("attach-options"), true
		case 4:
			return m.enter("manage"), true
		}
		return nil, true
	}
	if m.page == "upload" && m.focus == 4 {
		if m.pendingFile != "" {
			m.note = "Finish or abandon this upload before changing its options."
			return nil, true
		}
		m.optionsBack = "upload"
		return m.enter("attach-options"), true
	}
	return nil, false
}

func (m *workspace) activateManagementMenu(action string) tea.Cmd {
	id := m.store.ID
	switch action {
	case "Store details":
		return m.openDetails("Store details", storeDetails(m.store, time.Now()), "manage")
	case "Rename store", "Expiration", "Edit metadata", "Attach existing files":
		if !m.mutable() {
			return nil
		}
		switch action {
		case "Rename store":
			return m.enter("rename")
		case "Expiration":
			return m.enter("expiration")
		case "Edit metadata":
			return m.enter("metadata")
		default:
			m.sourceFiles, m.sourceNext, m.selectedSources = nil, "", map[string]bool{}
			m.enter("sources")
			return m.loadSources(false)
		}
	case "Open file batch":
		return m.enter("open-batch")
	case "Delete store":
		return m.enter("delete-store")
	case "File details":
		return m.openDetails("File details", []detailField{
			{"ID", m.file.ID}, {"Status", string(m.file.Status)},
			{"Size", fmt.Sprintf("%d bytes", m.file.UsageBytes)}, {"Created", displayTimestamp(m.file.CreatedAt)},
			{"Chunking", compactJSON(m.file.ChunkingStrategy)}, {"Attributes", compactJSON(m.file.Attributes)},
			{"Last error", compactJSON(m.file.LastError)},
		}, "file")
	case "Read content":
		file := m.file.ID
		return m.start("content", "Reading indexed content…", func(ctx context.Context) workMessage {
			text, err := m.manager().FileContent(ctx, id, file)
			return workMessage{managementResult: managementResult{text: text}, err: err}
		})
	case "Edit attributes", "Remove attachment":
		if !m.mutable() {
			return nil
		}
		if action == "Edit attributes" {
			return m.enter("attributes")
		}
		return m.enter("remove-file")
	case "Batch details":
		return m.openDetails("Batch details", []detailField{
			{"ID", m.batch.ID}, {"Status", string(m.batch.Status)},
			{"Created", displayTimestamp(m.batch.CreatedAt)}, {"File counts", compactJSON(m.batch.FileCounts)},
		}, "batch")
	case "Batch files":
		m.files, m.fileNext = nil, ""
		m.enter("batchfiles")
		return m.loadFiles(false)
	case "Refresh status":
		return m.getBatch(m.batch.ID)
	case "Cancel processing":
		if !m.mutable() {
			return nil
		}
		return m.enter("cancel-batch")
	case "Back":
		return m.back()
	}
	return nil
}

func displayTimestamp(value int64) string {
	if value == 0 {
		return "Not set"
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func (m *workspace) getBatch(id string) tea.Cmd {
	store := m.store.ID
	return m.start("batch", "Reading batch status…", func(ctx context.Context) workMessage {
		batch, err := m.manager().GetBatch(ctx, store, id)
		return workMessage{managementResult: managementResult{batch: batch}, err: err}
	})
}

func (m *workspace) submitManagement() tea.Cmd {
	page, value := m.page, m.editor.Value()
	if page == "filter" {
		m.storeFilter = value
		return m.enter("stores")
	}
	if page == "attach-options" {
		opts, err := parseAttachOptions(value)
		if err != nil {
			m.note = err.Error()
			return nil
		}
		*m.editableAttachOptions() = opts
		return m.enterWithNote(m.optionsBack, "Attachment options saved.")
	}
	if page == "open-batch" {
		if strings.TrimSpace(value) == "" {
			m.note = "Enter a batch ID."
			return nil
		}
		return m.getBatch(strings.TrimSpace(value))
	}
	id := m.store.ID
	if page == "delete-store" || page == "remove-file" || page == "cancel-batch" {
		target := id
		if page == "remove-file" {
			target = m.file.ID
		} else if page == "cancel-batch" {
			target = m.batch.ID
		}
		if value != target {
			m.note = "The ID does not match. No request was sent."
			return nil
		}
		if page != "delete-store" && !m.mutable() {
			return nil
		}
		return m.start(page, "Applying confirmed action…", func(ctx context.Context) workMessage {
			if page == "delete-store" {
				return workMessage{err: m.manager().DeleteStore(ctx, id)}
			}
			if page == "remove-file" {
				return workMessage{err: m.manager().RemoveFile(ctx, id, target)}
			}
			batch, err := m.manager().CancelBatch(ctx, id, target)
			return workMessage{managementResult: managementResult{batch: batch}, err: err}
		})
	}
	if !m.mutable() {
		return nil
	}
	changes := map[string]any{}
	switch page {
	case "rename":
		if strings.TrimSpace(value) == "" {
			m.note = "Enter a store name."
			return nil
		}
		changes["name"] = value
	case "expiration":
		if strings.EqualFold(strings.TrimSpace(value), "never") {
			changes["expires_after"] = nil
		} else {
			days, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || days < 1 {
				m.note = "Enter a positive number of days, or never."
				return nil
			}
			changes["expires_after"] = map[string]any{"anchor": "last_active_at", "days": days}
		}
	case "metadata", "attributes":
		values, err := decodePairs(value, page == "attributes")
		if err != nil {
			m.note = err.Error()
			return nil
		}
		if page == "attributes" {
			file := m.file.ID
			return m.start("attributes", "Updating file attributes…", func(ctx context.Context) workMessage {
				result, err := m.manager().UpdateFile(ctx, id, file, values)
				return workMessage{attached: result, err: err}
			})
		}
		changes["metadata"] = values
	}
	return m.start("update-store", "Updating store…", func(ctx context.Context) workMessage {
		store, err := m.manager().UpdateStore(ctx, id, changes)
		return workMessage{store: store, err: err}
	})
}

func (m *workspace) completeManagement(msg workMessage) (tea.Cmd, bool) {
	switch msg.kind {
	case "update-store":
		m.setStore(msg.store)
		return m.enterWithNote("manage", "Store updated."), true
	case "delete-store":
		id := m.store.ID
		kept := m.stores[:0]
		for _, store := range m.stores {
			if store.ID != id {
				kept = append(kept, store)
			}
		}
		m.stores, m.store, m.files, m.results = kept, nil, nil, nil
		delete(m.retainedUploads, id)
		m.pendingFile, m.pendingName, m.query, m.submittedQuery = "", "", "", ""
		m.pendingAttached, m.searched, m.moreResults = false, false, false
		m.managementState = managementState{}
		return m.enterWithNote("stores", "Store deleted. Source files remain in Files storage."), true
	case "remove-file":
		kept := m.files[:0]
		for _, file := range m.files {
			if file.ID != m.file.ID {
				kept = append(kept, file)
			}
		}
		m.files, m.results, m.searched, m.moreResults = kept, nil, false, false
		if m.pendingFile == m.file.ID {
			m.pendingFile, m.pendingName, m.pendingAttached = "", "", false
			m.attachOptions = AttachOptions{}
		}
		return m.enterWithNote(m.fileOrigin, "Attachment removed. The source file remains in Files storage."), true
	case "attributes":
		m.file = msg.attached
		for i := range m.files {
			if m.files[i].ID == msg.attached.ID {
				m.files[i] = *msg.attached
			}
		}
		m.results, m.searched, m.moreResults = nil, false, false
		return m.enterWithNote("file", "File attributes updated."), true
	case "batch-create", "cancel-batch":
		if msg.kind == "batch-create" {
			m.batchOptions = AttachOptions{}
		}
		m.batch = msg.batch
		m.selectedSources = map[string]bool{}
		return m.enterWithNote("batch", "Batch status: "+string(msg.batch.Status)), true
	case "manage-store":
		m.setStore(msg.store)
		return m.enter("manage"), true
	case "file-detail":
		m.file = msg.attached
		return m.enter("file"), true
	case "sources":
		if msg.reset {
			m.sourceFiles = nil
			m.selected = 0
		}
		m.sourceFiles = append(m.sourceFiles, msg.sources.Files...)
		m.sourceNext = msg.sources.Next
		for _, file := range msg.sources.Files {
			m.names[file.ID] = file.Filename
		}
		return nil, true
	case "batch":
		m.batch = msg.batch
		return m.enter("batch"), true
	case "content":
		text := msg.text
		if text == "" {
			text = "No indexed text was returned."
		}
		return m.openDocument("Indexed file content", text, "file"), true
	}
	return nil, false
}

func managementMutation(kind string) bool {
	switch kind {
	case "update-store", "delete-store", "attributes", "remove-file", "batch-create", "cancel-batch":
		return true
	}
	return false
}
