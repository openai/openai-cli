package listview

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
)

// Print each page before requesting the next. No terminal modes are changed.
func printPages(opts Options, width int, fetch func() (Page, error)) error {
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	var table printedTable
	for {
		if err := opts.Context.Err(); err != nil {
			return err
		}
		page, fetchErr := fetch()
		if err := table.writePage(out, opts, page, width, fetchErr); err != nil {
			return errors.Join(fetchErr, err)
		}
		if fetchErr != nil || !page.More {
			return fetchErr
		}
	}
}

// Finished pages live in scrollback. Retain only the layout between fetches.
type printedTable struct {
	layout    readable.ListTableLayout
	labeled   bool
	wroteRows bool
}

// Keep the committed header and notice policy across print-only and interactive pages.
func (t *printedTable) writePage(out io.StringWriter, opts Options, page Page, width int, fetchErr error) error {
	if len(page.Items) > 0 {
		if file, ok := opts.Stdout.(*os.File); ok {
			if current, _, sizeErr := term.GetSize(file.Fd()); sizeErr == nil {
				width = current
			}
		}
		content, continuation, err := t.render(opts, page, width)
		if err != nil {
			return err
		}
		if t.wroteRows && !continuation {
			content = "\n" + content
		}
		if _, err := out.WriteString(content); err != nil {
			return err
		}
		t.wroteRows = true
	}
	notice := page.Notice
	if !t.wroteRows && !page.More && fetchErr == nil && notice == "" {
		notice = "No results."
	}
	if t.wroteRows && !page.More && fetchErr == nil && notice == "" {
		notice = opts.Hint
	}
	if notice != "" {
		if _, err := out.WriteString(notice + "\n"); err != nil {
			return err
		}
	}
	return nil
}

func (t *printedTable) render(opts Options, page Page, width int) (content string, continuation bool, err error) {
	if opts.ProjectPrintTable == nil || !t.wroteRows && !page.More {
		content, err = printablePage(opts, page, width)
		return content, false, err
	}
	if !t.labeled && !page.FullLabels {
		projection, err := opts.ProjectPrintTable(page.Items)
		if err != nil {
			return "", false, err
		}
		content, fits := t.layout.Render(projection.Headers, projection.Rows, width, projection.ProtectedColumn, projection.FullValues)
		if fits {
			if !t.wroteRows {
				content = opts.Style.Table(content)
			}
			return content, t.wroteRows, nil
		}
	}
	// Later rows cannot safely sit under an obsolete table header.
	t.labeled = true
	content, err = printableLabels(opts, page)
	return content, false, err
}

func printablePage(opts Options, page Page, width int) (string, error) {
	if !page.FullLabels {
		if !opts.PrintOnly {
			return opts.RenderPage(page.Items, width)
		}
		table, fits, err := opts.RenderTable(page.Items, width)
		if fits || err != nil {
			return table, err
		}
	}
	return printableLabels(opts, page)
}

func printableLabels(opts Options, page Page) (string, error) {
	var text strings.Builder
	for i, item := range page.Items {
		detail, err := opts.RenderRecordLabels(item, page.FullLabels)
		if err != nil {
			return "", err
		}
		if i != 0 {
			text.WriteByte('\n')
		}
		text.WriteString(detail)
	}
	return text.String(), nil
}
