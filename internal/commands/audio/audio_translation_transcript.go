package audio

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

// Page the same sealed files used by Save. Remember offsets without retaining past page text.
type translationTranscript struct {
	original bool
	reader   *io.SectionReader
	starts   []int64
	next     int64
	rows     []string
}

func (m *terminalTranslationModel) openTranscript(original bool) {
	name := "translation.txt"
	if original {
		name = "original.txt"
	}
	reader, err := m.result.stage.reader(name)
	if err != nil {
		m.lastErr, m.note = err, err.Error()
		m.transcript = nil
		return
	}
	m.transcript = &translationTranscript{original: original, reader: reader, starts: []int64{0}}
	m.readTranscript()
}

func (m *terminalTranslationModel) readTranscript() {
	p := m.transcript
	start := p.starts[len(p.starts)-1]
	layout := m.translationLayout()
	rows := max(1, min(100, layout.height-3-len(layout.transcriptFooter())))
	reader := bufio.NewReader(io.NewSectionReader(p.reader, start, p.reader.Size()-start))
	p.rows, p.next = nil, start
	var row strings.Builder
	cells := 0
	for len(p.rows) < rows && p.next < p.reader.Size() {
		r, n, err := reader.ReadRune()
		if err != nil {
			m.note = "Could not read this transcript page."
			m.lastErr = audioFailure(m.note, err)
			m.transcript = nil
			return
		}
		if r == '\n' {
			p.rows = append(p.rows, row.String())
			row.Reset()
			cells = 0
		} else {
			safe := readable.Text(string(r))
			if r == '\t' {
				safe = "    "
			}
			w := ansi.StringWidth(safe)
			// Bound each display fragment, even for unbroken runs of zero-width characters.
			if (cells+w > layout.width || row.Len() >= 32*1024) && row.Len() > 0 {
				p.rows = append(p.rows, row.String())
				row.Reset()
				cells = 0
				if len(p.rows) == rows {
					break
				}
			}
			row.WriteString(safe)
			cells += w
		}
		p.next += int64(n)
	}
	if row.Len() > 0 {
		p.rows = append(p.rows, row.String())
	}
}

func (m *terminalTranslationModel) transcriptKey(key string) {
	p := m.transcript
	switch key {
	case "esc", "t", "q":
		m.transcript = nil
	case "tab", "shift+tab", "left", "right":
		m.openTranscript(!p.original)
	case "down", "pgdown", "space":
		if p.next < p.reader.Size() {
			p.starts = append(p.starts, p.next)
			m.readTranscript()
		}
	case "up", "pgup":
		if len(p.starts) > 1 {
			p.starts = p.starts[:len(p.starts)-1]
			m.readTranscript()
		}
	}
}

func (m *terminalTranslationModel) transcriptView(l translationLayout) tea.View {
	p := m.transcript
	name := "Translation"
	if p.original {
		name = "Original"
	}
	header := l.heading(l.title.Render(name), l.muted.Render(fmt.Sprintf("Page %d", len(p.starts))))
	rows := []string{header, l.divider, ""}
	if p.reader.Size() == 0 {
		rows = append(rows, l.muted.Render("No captions for this track."))
	}
	rows = append(rows, p.rows...)
	return l.panel(rows, l.transcriptFooter(), nil)
}

func (l translationLayout) transcriptFooter() []string {
	return l.footer([2]string{"↑↓", "page"}, [2]string{"Tab", "switch track"}, [2]string{"Esc", "back"})
}
