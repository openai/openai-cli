package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Presented failures retain their original cause and exit status without opening
// a second error screen after the user has already read the first one.
type batchBrowserPresentedError struct{ error }

func (e *batchBrowserPresentedError) Unwrap() error { return e.error }

type batchBrowserPrintError struct{ error }

func (e *batchBrowserPrintError) Unwrap() error { return e.error }

func (w workflow) browserFailureText(cmd *cli.Command, err error, action, id string) string {
	message := "The action could not finish.\nNext: Check your access and connection, then retry this action."
	var local *WorkflowError
	var apierr *openai.Error
	if errors.As(err, &local) && local.submission != nil {
		message = local.Error()
	} else if errors.As(err, &apierr) && w.options.APIErrorMessage != nil {
		message = w.options.APIErrorMessage(cmd.Root(), err, apierr)
	} else if errors.As(err, &local) {
		message = local.Error()
	}
	details := "Action: " + batchBrowserActionLabel(action)
	if id != "" {
		details += "\nBatch: " + batchInputLabel(id)
	}
	return readable.Text(details + "\n\n" + message + "\n")
}

func (w workflow) showBatchBrowserFailure(ctx context.Context, cmd *cli.Command, failure error, action, id string, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) (err error) {
	document, err := newBatchBrowserDocument(ctx)
	if err != nil {
		return errors.Join(failure, err)
	}
	defer func() { err = errors.Join(err, document.close()) }()
	if _, err = io.WriteString(document, w.browserFailureText(cmd, failure, action, id)); err != nil {
		return errors.Join(failure, err)
	}
	return showBatchBrowserDocument(ctx, cmd, document, "Action failed", id, show, beforeOutput)
}

func (w workflow) runBatchBrowserAction(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, id string, actions *batchBrowserScreen, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) (err error) {
	document, err := newBatchBrowserDocument(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, document.close()) }()
	// Keep the existing projections and complete record streaming. Only the
	// destination changes; command flags and ordinary command output do not.
	presentation := w
	presentation.browserOutput = document
	terminalFailed := false
	showAction := func(ctx context.Context, screen *batchBrowserScreen) error {
		err := show(ctx, screen)
		terminalFailed = terminalFailed || err != nil
		return err
	}
	failure := presentation.renderBatchBrowserAction(ctx, cmd, client, options, id, actions, showAction, beforeOutput, document)
	var printFailure *batchBrowserPrintError
	if errors.As(failure, &printFailure) {
		return failure
	}
	if terminalFailed || ctx.Err() != nil {
		return failure
	}
	title := batchBrowserActionLabel(actions.chosen)
	if failure != nil {
		title = "Action failed"
		if document.hasContent() {
			_, err = io.WriteString(document, "\nAction failed\n\n")
		}
		if err == nil {
			_, err = io.WriteString(document, w.browserFailureText(cmd, failure, actions.chosen, id))
		}
		if err != nil {
			return errors.Join(failure, err)
		}
	}
	if document.hasContent() {
		if failure == nil && actions.chosen == "status" {
			// Batch details already includes the complete, wrappable ID in the document and its printed copy.
			id = ""
		}
		if err = showBatchBrowserDocument(ctx, cmd, document, title, id, show, beforeOutput); err != nil {
			return errors.Join(failure, err)
		}
	}
	if failure != nil {
		return &batchBrowserPresentedError{failure}
	}
	return nil
}

func showBatchBrowserDocument(ctx context.Context, cmd *cli.Command, document *batchBrowserDocument, title, id string, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) error {
	screen := newBatchBrowserDocumentScreen(ctx, document, title, id)
	defer screen.documentState.stop()
	for {
		if err := show(ctx, screen); err != nil {
			return err
		}
		if screen.documentState.err != nil {
			return screen.documentState.err
		}
		if screen.chosen != "print" {
			return nil
		}
		if err := beforeOutput(ctx); err != nil {
			return err
		}
		if err := writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error { return document.writeTo(ctx, out) }); err != nil {
			return &batchBrowserPrintError{err}
		}
		if err := show(ctx, newBatchBrowserReturn("Printed to terminal scrollback.")); err != nil {
			return err
		}
	}
}

type batchBrowserDocumentState struct {
	ctx        context.Context
	cancel     context.CancelFunc
	sequence   uint64
	page       batchBrowserDocumentPage
	loading    bool
	err        error
	csvResults bool
}

func (s *batchBrowserDocumentState) stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

type batchBrowserDocumentReady struct {
	screen   *batchBrowserScreen
	sequence uint64
	page     batchBrowserDocumentPage
	err      error
}

func newBatchBrowserDocumentScreen(ctx context.Context, document *batchBrowserDocument, title, id string) *batchBrowserScreen {
	m := newBatchBrowserScreen("document", title, id, nil)
	m.document, m.documentState = document, &batchBrowserDocumentState{ctx: ctx}
	return m
}

// Match the shared frame's heading, optional subtitle, divider, and footer.
func (m *batchBrowserScreen) documentSize() (int, int) {
	width, height := max(1, m.width), max(1, m.height)
	if width >= 50 && height >= 12 {
		width, height = width-2, height-2
	}
	footer, used := 2, 2
	keys := []string{"↑↓ scroll", "PgUp/PgDn page", "p print", "Esc back"}
	if m.documentState.csvResults {
		keys[2] = "s save CSV"
		keys = append(keys[:3], "Ctrl+S save as", keys[3])
	}
	if m.documentRawAvailable {
		label := "j Raw JSON"
		if m.documentRaw {
			label = "j Readable"
		}
		keys = append(keys[:3], label, keys[3])
	}
	for _, key := range keys {
		gap := 0
		if used > 2 {
			gap = 2
		}
		if used+gap+ansi.StringWidth(key) > width {
			footer++
			used = 2
			gap = 0
		}
		used += gap + ansi.StringWidth(key)
	}
	content := width - 2
	if m.width >= 50 && m.height >= 12 {
		content--
	}
	header := 2
	if m.subtitle != "" {
		header++
	}
	return max(1, content), max(1, height-header-footer)
}

func (m *batchBrowserScreen) loadDocument(operation string, first int64) tea.Cmd {
	s := m.documentState
	s.stop()
	s.sequence++
	sequence := s.sequence
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel, s.loading = cancel, true
	width, rows := m.documentSize()
	offset := s.page.Offset
	document := m.document
	return func() tea.Msg {
		var page batchBrowserDocumentPage
		var err error
		switch operation {
		case "end":
			page, err = document.tail(ctx, width, rows)
		case "anchor":
			page, err = document.pageAtOffset(ctx, width, offset, rows)
		default:
			page, err = document.page(ctx, width, first, rows)
		}
		return batchBrowserDocumentReady{m, sequence, page, err}
	}
}

func (m *batchBrowserScreen) updateDocument(message tea.Msg) (tea.Model, tea.Cmd) {
	s := m.documentState
	switch msg := message.(type) {
	case batchBrowserDocumentReady:
		if msg.screen != m || msg.sequence != s.sequence {
			return m, nil
		}
		s.loading = false
		if msg.err != nil {
			s.err, m.closed = msg.err, true
			return m, tea.Quit
		}
		s.page = msg.page
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.loadDocument("anchor", 0)
	case tea.KeyPressMsg:
		_, rows := m.documentSize()
		if s.csvResults {
			switch msg.String() {
			case "s", "ctrl+s":
				s.stop()
				m.closed, m.chosen = true, "save"
				if msg.String() == "ctrl+s" {
					m.chosen = "save-as"
				}
				return m, tea.Quit
			case "p":
				return m, nil
			}
		}
		if msg.String() == "j" && m.documentRawAvailable {
			s.stop()
			m.closed, m.chosen = true, "toggle_raw"
			return m, tea.Quit
		}
		switch msg.String() {
		case "ctrl+c", "esc", "enter", "p":
			s.stop()
			m.closed, m.chosen = true, "back"
			if msg.String() == "p" {
				m.chosen = "print"
			}
			if msg.String() == "ctrl+c" {
				m.chosen = ""
			}
			return m, tea.Quit
		case "up", "k":
			return m, m.loadDocument("page", max(0, s.page.First-1))
		case "down", "j":
			if s.page.More {
				return m, m.loadDocument("page", s.page.First+1)
			}
		case "pgup", "b":
			return m, m.loadDocument("page", max(0, s.page.First-int64(rows)))
		case "pgdown", "space", "f":
			if s.page.More {
				return m, m.loadDocument("page", s.page.First+int64(rows))
			}
		case "home", "g":
			return m, m.loadDocument("page", 0)
		case "end", "G":
			return m, m.loadDocument("end", 0)
		}
	}
	return m, nil
}

func (m *batchBrowserScreen) documentLines(height, width int, footer []string, rule func() string, muted, accent lipgloss.Style, semantic func(lipgloss.Style, string) lipgloss.Style, colors bool) []string {
	s := m.documentState
	heading := accent.Bold(colors)
	position := ""
	if s.loading {
		position = "Loading…"
	} else if s.page.More || s.page.First > 0 {
		position = fmt.Sprintf("Lines %d–%d", s.page.First+1, s.page.First+int64(len(s.page.Lines)))
		if !s.page.More {
			position += "  End"
		}
	}
	position = ansi.Truncate(position, max(0, width-14), "…")
	titleWidth := width - ansi.StringWidth(position)
	title := ansi.Truncate(batchInputLabel(m.title), max(1, titleWidth-2), "…")
	lines := []string{"  " + lipgloss.NewStyle().Bold(colors).Render(title) + strings.Repeat(" ", max(0, titleWidth-ansi.StringWidth(title))) + muted.Render(position)}
	if m.subtitle != "" {
		lines = append(lines, "  "+muted.Render(ansi.Truncate(batchInputLabel(m.subtitle), width, "…")))
	}
	lines = append(lines, rule())
	for index, text := range s.page.Lines[:min(len(s.page.Lines), max(0, height-len(lines)-len(footer)))] {
		// These are presentation cues, never inferred request outcomes. Unknown
		// values and continuation rows remain ordinary text.
		if s.page.Styles != nil {
			var styled strings.Builder
			position := int64(0)
			for _, span := range s.page.Styles[index] {
				style := lipgloss.NewStyle()
				switch span.Role {
				case "identity":
					style = style.Bold(colors)
				case "heading":
					style = heading
				case "secondary":
					style = muted
				default:
					style = semantic(style, span.Role)
				}
				styled.WriteString(text[position:span.Start])
				styled.WriteString(style.Render(text[span.Start:span.End]))
				position = span.End
			}
			styled.WriteString(text[position:])
			text = styled.String()
		} else if s.csvResults && strings.HasPrefix(text, "Row ") {
			text = heading.Render(text)
		} else if s.csvResults && strings.HasPrefix(text, "Answer: ") {
			text = heading.Render("Answer: ") + strings.TrimPrefix(text, "Answer: ")
		} else if s.csvResults && strings.HasPrefix(text, "Error: ") {
			text = semantic(muted, "negative").Render("Error: ") + strings.TrimPrefix(text, "Error: ")
		} else if strings.HasPrefix(text, "Request: ") {
			text = heading.Render(text)
		} else if label, value, ok := strings.Cut(text, ": "); ok {
			text = muted.Render(label+": ") + value
		}
		// A resize can draw the previous page while its replacement loads.
		lines = append(lines, "  "+ansi.Truncate(text, width, "…"))
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	return append(lines, footer...)
}
