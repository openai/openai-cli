package listview

import (
	"errors"
	"strings"

	clioutput "github.com/openai/openai-cli/internal/output"
)

// Print each page before requesting the next. No terminal modes are changed.
func printPages(opts Options, width int, fetch func() (Page, error)) error {
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	wroteRows := false
	for {
		if err := opts.Context.Err(); err != nil {
			return err
		}
		page, fetchErr := fetch()
		if len(page.Items) > 0 {
			content, err := printablePage(opts, page, width)
			if err != nil {
				return errors.Join(fetchErr, err)
			}
			if wroteRows {
				content = "\n" + content
			}
			if _, err := out.WriteString(content); err != nil {
				return errors.Join(fetchErr, err)
			}
			wroteRows = true
		}
		notice := page.Notice
		if !wroteRows && !page.More && fetchErr == nil && notice == "" {
			notice = "No results."
		}
		if wroteRows && !page.More && fetchErr == nil && notice == "" {
			notice = opts.Hint
		}
		if notice != "" {
			if _, err := out.WriteString(notice + "\n"); err != nil {
				return errors.Join(fetchErr, err)
			}
		}
		if fetchErr != nil || !page.More {
			return fetchErr
		}
	}
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
