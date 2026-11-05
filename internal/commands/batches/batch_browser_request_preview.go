package batches

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type batchBrowserInlinePreview struct {
	read     func(context.Context, int64) ([]batchBrowserPreviewLine, error)
	cancel   context.CancelFunc
	sequence uint64
	lines    []batchBrowserPreviewLine
	loading  bool
}

func (p *batchBrowserInlinePreview) stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.sequence++
	p.lines = nil
	p.loading = false
}

type batchBrowserPreviewReady struct {
	screen   *batchBrowserScreen
	sequence uint64
	lines    []batchBrowserPreviewLine
	err      error
}

func (m *batchBrowserScreen) loadRequestPreview() tea.Cmd {
	s := m.requestState
	s.preview.stop()
	index := s.selected - s.page.First
	if !s.expanded || s.preview.read == nil || index < 0 || index >= int64(len(s.page.Rows)) {
		return nil
	}
	id, read, sequence := s.page.Rows[index].ID, s.preview.read, s.preview.sequence
	ctx, cancel := context.WithCancel(s.ctx)
	s.preview.cancel, s.preview.loading = cancel, true
	return func() tea.Msg {
		lines, err := read(ctx, id)
		return batchBrowserPreviewReady{m, sequence, lines, err}
	}
}

func (m *batchBrowserScreen) requestPreviewRows() int {
	if !m.requestState.expanded {
		return 0
	}
	// Reserve at least one selectable request at every supported size.
	return min(5, m.requestRows()-1)
}

type batchBrowserPreviewLine struct {
	text, role string
}

func (m *batchBrowserScreen) requestPreviewLines(row batchBrowserRecordRow, width, shownWidth int, muted lipgloss.Style, semantic func(lipgloss.Style, string) lipgloss.Style) []string {
	budget := m.requestPreviewRows()
	var result []string
	appendLine := func(label string, line batchBrowserPreviewLine) {
		style := semantic(lipgloss.NewStyle(), line.role)
		if line.role == "secondary" || line.role == "heading" {
			style = muted
		}
		prefix := ""
		if label != "" {
			prefix = label + strings.Repeat(" ", max(2, 14-ansi.StringWidth(label)))
		}
		for i, text := range strings.Split(ansi.Wrap(batchInputLabel(line.text), max(1, width-2-ansi.StringWidth(prefix)), ""), "\n") {
			if len(result) >= budget {
				return
			}
			indent := prefix
			if i > 0 {
				indent = strings.Repeat(" ", ansi.StringWidth(prefix))
			}
			result = append(result, "    "+muted.Render(indent)+style.Render(text))
		}
	}
	lines := m.requestState.preview.lines
	// Keep the selected answer visible when the table omits or truncates its value.
	if shownWidth < ansi.StringWidth(row.Preview) {
		appendLine("", batchBrowserPreviewLine{text: row.Preview})
	}
	for i := 0; i < len(lines) && len(result) < budget; i++ {
		line := lines[i]
		if line.role == "record_message" && line.text == row.Preview {
			continue // The row or its wrapped continuation already shows the API message.
		}
		if line.role == "heading" && line.text == "Settings" && i+1 < len(lines) && lines[i+1].role == "field" {
			continue
		}
		if line.role == "heading" || line.role == "field" {
			if i+1 >= len(lines) || lines[i+1].role != "" {
				continue // An excerpt can end in the middle of a section.
			}
			i++
			body := lines[i]
			if (line.text == "Input" || line.text == "Output" || line.text == "Prompt") && body.text == row.Preview {
				continue
			}
			appendLine(line.text, body)
		} else {
			appendLine("", line)
		}
	}
	if len(result) == 0 && budget > 0 && !m.requestState.preview.loading {
		appendLine("", batchBrowserPreviewLine{text: "See full record", role: "secondary"})
	}
	return result
}

// Limit the preview only. The full record viewer still streams every record.
var errBatchPreviewFull = errors.New("batch preview is full")

type batchBrowserPreviewText struct {
	text        strings.Builder
	identityEnd int
	roles       []RecordTextStyle
}

func (w *batchBrowserPreviewText) Write(p []byte) (int, error) {
	n, err := w.text.Write(p[:min(len(p), 4096-w.text.Len())])
	if n < len(p) {
		return n, errBatchPreviewFull
	}
	return n, err
}

func (w *batchBrowserPreviewText) decorate(style RecordTextStyle) {
	if style.Start < 0 || style.End > int64(w.text.Len()) {
		return
	}
	if style.Role == "identity" && style.Start == 0 {
		w.identityEnd = int(style.End)
	} else {
		w.roles = append(w.roles, style)
	}
}

func (w *batchBrowserPreviewText) lines() []batchBrowserPreviewLine {
	var lines []batchBrowserPreviewLine
	offset := w.identityEnd
	for line := range strings.SplitSeq(w.text.String()[offset:], "\n") {
		text := strings.TrimSpace(line)
		if text != "" {
			role := ""
			start := int64(offset + strings.Index(line, text))
			for _, span := range w.roles {
				if start >= span.Start && start+int64(len(text)) <= span.End {
					role = span.Role
					break
				}
				if start == span.Start && span.End < start+int64(len(text)) && (span.Role == "heading" || span.Role == "secondary") {
					// The projection decorated just the label, such as "Next" or "Request ID".
					length := int(span.End - start)
					label := strings.TrimSuffix(strings.TrimSpace(text[:length]), ":")
					lines = append(lines, batchBrowserPreviewLine{label, "field"})
					text = strings.TrimSpace(text[length:])
					break
				}
			}
			lines = append(lines, batchBrowserPreviewLine{strings.ToValidUTF8(text, "�"), role})
		}
		offset += len(line) + 1
	}
	return lines
}
