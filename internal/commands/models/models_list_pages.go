package models

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
)

// ShowPages opens the terminal before requesting page one. The pager owns and
// joins the request worker, including when the user quits during a body read.
func ShowPages(opts listview.Options, client openai.Client, options []option.RequestOption, diagnostics bool) error {
	ctx, cancel := context.WithCancel(opts.Context)
	defer cancel()
	pages := &modelRequestPages{ctx: ctx, client: client, options: options}
	render := opts.RenderPage
	opts.RenderPage = func(items []gjson.Result, width int) (string, error) {
		total := -1 // Cursor responses need not count the entire catalog.
		if pages.local != nil {
			if pages.local.size >= pages.total {
				return render(items, width)
			}
			total = pages.total
		}
		content, supported, err := renderTable(ctx, items, width, diagnostics, opts.Style, total)
		if err != nil || supported {
			return content, err
		}
		return render(items, width)
	}
	return listview.Run(opts, pages.next, cancel)
}

type modelRequestPages struct {
	ctx     context.Context
	client  openai.Client
	options []option.RequestOption
	cursor  string
	seen    map[string]bool
	done    bool
	local   *modelPages
	total   int
}

func (p *modelRequestPages) next() (listview.Page, error) {
	if err := p.ctx.Err(); err != nil {
		return listview.Page{}, err
	}
	if p.local != nil {
		return p.local.next()
	}
	if p.done {
		return listview.Page{}, nil
	}
	body, err := p.request(true)
	if p.cursor == "" && unsupportedModelPageLimit(err) {
		// Strict legacy servers may reject the optional limit. Retry only its
		// specific unsupported-parameter error, before receiving any records.
		body, err = p.request(false)
	}
	if err != nil {
		return listview.Page{}, err
	}

	object := body.Get("object").Str
	hasMore, next := body.Get("has_more"), body.Get("next")
	legacy := object == "list" && p.cursor == "" &&
		(!hasMore.Exists() || hasMore.Type == gjson.False) && next.Type == gjson.Null
	if object != "page" && !legacy {
		// A legacy replica can return the entire catalog for a continuation.
		// Its records do not belong to this page.
		return listview.Page{}, &ListError{"Models returned an unexpected response type. Output may be incomplete."}
	}
	data := body.Get("data")
	if !data.IsArray() {
		return listview.Page{}, &ListError{"Models returned an invalid list response."}
	}
	items := data.Array()
	if legacy {
		// Legacy servers ignore the optional limit and return the complete list.
		// Reuse this exact response, including its original fallback for malformed IDs.
		local, err := localModelPages(p.ctx, items)
		if err != nil {
			return listview.Page{}, err
		}
		p.local, p.total = &local, len(items)
		return p.local.next()
	}

	page := listview.Page{Items: items}
	if hasMore.Type != gjson.True && hasMore.Type != gjson.False {
		return page, &ListError{"Models returned invalid pagination metadata. Output may be incomplete."}
	}
	if hasMore.Type == gjson.False {
		p.done = true
		if !next.Exists() || next.Type != gjson.Null {
			return page, &ListError{"Models returned an unexpected continuation cursor. Output may be incomplete."}
		}
		return page, nil
	}
	if len(items) == 0 || next.Type != gjson.String || next.Str == "" || p.seen[next.Str] {
		return page, &listview.PaginationError{}
	}
	if p.seen == nil {
		p.seen = make(map[string]bool)
	}
	p.cursor = next.Str
	p.seen[p.cursor] = true
	page.More = true
	return page, nil
}

func (p *modelRequestPages) request(paginated bool) (gjson.Result, error) {
	options := slices.Clone(p.options)
	if paginated {
		options = append(options, option.WithQuery("limit", strconv.Itoa(modelListPageSize)))
		if p.cursor != "" {
			// A cursor is data. The SDK encodes it on the original Models route.
			options = append(options, option.WithQuery("page", p.cursor))
		}
	}
	// The pager reads the original envelope, including fields outside the SDK model.
	// Ask the SDK to validate JSON and content type without decoding every model twice.
	var raw json.RawMessage
	options = append(options, option.WithResponseBodyInto(&raw))
	if _, err := p.client.Models.List(p.ctx, options...); err != nil {
		return gjson.Result{}, err
	}
	body := gjson.ParseBytes(raw)
	if body.Type == gjson.Null {
		return gjson.Result{}, &ListError{"Models returned an invalid list response."}
	}
	return body, nil
}

func unsupportedModelPageLimit(err error) bool {
	var apiError *openai.Error
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusBadRequest &&
		apiError.Param == "limit" && (apiError.Code == "unknown_parameter" || apiError.Code == "unsupported_parameter")
}
