package app

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const listPagerMetadata = "interactive-list-pager"

func markListPager(command *cli.Command) {
	if command.Metadata == nil {
		command.Metadata = make(map[string]any)
	}
	command.Metadata[listPagerMetadata] = true
}

// Decorate list actions once, including actions added later during startup.
func configureRegisteredLists(root *cli.Command) {
	var visit func(*cli.Command, string)
	visit = func(parent *cli.Command, parentPath string) {
		// Compatibility routes can expose the same generated resource under
		// a shorter path. Use its canonical schema for every spelling.
		resourcePath := parentPath
		if canonical := localcommands.ResourceName(parent); canonical != "" {
			resourcePath = strings.ReplaceAll(canonical, ":", " ")
		}
		for _, child := range parent.Commands {
			path := strings.TrimSpace(parentPath + " " + strings.ReplaceAll(child.Name, ":", " "))
			for _, name := range child.Names() {
				if name == "ls" {
					name = "list"
				}
				if name == "list" || strings.HasPrefix(name, "list-") {
					registerListPager(child, resourcePath, path, name)
					break
				}
			}
			visit(child, path)
		}
	}
	visit(root, "")
}

func registerListPager(command *cli.Command, parent, title, method string) {
	if command.Action == nil || command.Metadata[listPagerMetadata] == true {
		return
	}
	markListPager(command)
	// These resources already opened a pager before list tables were generalized.
	browse := method == "list" && (parent == "files" || parent == "batches" || parent == "admin organization projects")
	help := "Terminal output prints all requested rows and returns to the shell. Use --browse for an interactive preview."
	if browse {
		help = listview.ControlsHelp
	}
	command.Description = strings.TrimSpace(command.Description + "\n\n" + help)
	if content, ok := command.Metadata["help-content"].(clihelp.Content); ok {
		content.Description = strings.TrimSpace(content.Description + "\n\n" + help)
		command.Metadata["help-content"] = content
	}

	resource := strings.ReplaceAll(strings.ReplaceAll(parent, " ", "."), "-", "_")
	operation := "(resource) " + resource + " > (method) " + strings.ReplaceAll(method, "-", "_")
	next := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("watch") || cmd.Bool("stream") || parent == "batches" && (cmd.IsSet("watch-count") || cmd.IsSet("poll-interval")) {
			return next(ctx, cmd)
		}
		opts := ShowJSONOpts{
			Context: ctx, Title: title, Operation: operation, OutputKind: OutputPageItem,
			Format: cmd.Root().String("format"), Transform: cmd.Root().String("transform"), RawOutput: cmd.Root().Bool("raw-output"),
		}
		// Generated limits use int64. Handwritten commands may use cli.IntFlag.
		if !InteractiveListOutput(opts) || cmd.IsSet("max-items") && fmt.Sprint(cmd.Value("max-items")) == "0" {
			return next(ctx, cmd)
		}
		opts.setDefaults()
		return runTerminalList(opts, func(opts ShowJSONOpts) error {
			return runRegisteredList(opts, func(ctx context.Context) error { return next(ctx, cmd) })
		})
	}
	configureListBrowsing(command, browse)
}

type registeredListKey struct{}

// The SDK hides page boundaries behind Next. Yield the records it has already
// decoded immediately before it makes its next request. This keeps cursor and
// retry handling in the SDK, and querying the next page under keyboard control.
type registeredList struct {
	rows        []gjson.Result
	notice      string
	fullLabels  bool
	yield       func(listview.Page, error) bool
	stopped     bool
	seen        map[string]bool
	page        *listPageContinuation
	rowIDCursor bool
}

func runRegisteredList(opts ShowJSONOpts, action func(context.Context) error) error {
	ctx, cancel := context.WithCancel(opts.Context)
	next, stop := iter.Pull2(registeredListPages(ctx, action))
	defer stop()
	defer cancel()
	return listview.Run(listViewerOptions(opts), func() (listview.Page, error) {
		page, err, _ := next()
		return page, err
	}, cancel)
}

func registeredListPages(ctx context.Context, action func(context.Context) error) iter.Seq2[listview.Page, error] {
	return func(yield func(listview.Page, error) bool) {
		source := &registeredList{yield: yield, seen: make(map[string]bool)}
		err := action(context.WithValue(ctx, registeredListKey{}, source))
		if !source.stopped {
			yield(listview.Page{Items: source.rows, Notice: source.notice, FullLabels: source.fullLabels}, err)
		}
	}
}

func (list *registeredList) yieldPending() bool {
	page := listview.Page{Items: list.rows, More: true}
	list.rows = nil
	list.stopped = !list.yield(page, nil)
	return !list.stopped
}

func currentRegisteredList(ctx context.Context) *registeredList {
	if ctx == nil {
		return nil
	}
	source, _ := ctx.Value(registeredListKey{}).(*registeredList)
	return source
}

func registeredListRequest(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	source := currentRegisteredList(request.Context())
	if source == nil {
		return next(request)
	}
	key := request.URL.RequestURI()
	if len(source.rows) > 0 {
		// CursorPage derives "after" from the last row ID. Other SDK
		// page types choose their own cursor independently of that query.
		if source.seen[key] || source.rowIDCursor && request.URL.Query().Get("after") == "" {
			return nil, &listview.PaginationError{}
		}
		if !source.yieldPending() {
			return nil, context.Canceled
		}
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response, err := next(request)
	if err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		source.seen[key] = true
		if response.Body != nil {
			source.page = &listPageContinuation{ReadCloser: response.Body}
			response.Body = source.page
		}
	}
	return response, err
}

// Local iterators have no HTTP boundary. Backpressure keeps even an unlimited
// producer responsive and lets quit stop it after at most one lookahead item.
const localListPageSize = 32

func collectListIterator[T any](list *registeredList, source jsonview.Iterator[T], maximum int64, opts ShowJSONOpts) error {
	if maximum == 0 {
		return source.Err()
	}
	_, list.rowIDCursor = source.(*pagination.CursorPageAutoPager[T])
	rows := clioutput.NewIterator(source, clioutput.IteratorOptions{
		Context: opts.Context, Transform: transformers.Identity, Limit: maximum,
	})
	remaining := maximum
	for rows.Next() {
		if len(list.seen) == 0 && len(list.rows) == localListPageSize && !list.yieldPending() {
			return context.Canceled
		}
		list.rows = append(list.rows, rows.Current().Result)
		if remaining > 0 {
			remaining--
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// ConversationCursorPage uses last_id. Its SDK iterator also stops if
	// that cursor is missing from a legacy response without has_more.
	_, usesLastID := source.(*pagination.ConversationCursorPageAutoPager[T])
	if remaining != 0 && list.page != nil &&
		(list.page.more || usesLastID && !list.page.hasMoreField && len(list.rows) > 0) {
		return &listview.PaginationError{}
	}
	return nil
}

func (list *registeredList) collectResponse(value gjson.Result, opts ShowJSONOpts) error {
	if opts.Operation == "(resource) webhooks.event_types > (method) list" {
		rows, notice, full, err := transformers.WebhookEventTypeRows(opts.Context, value)
		list.rows = append(list.rows, rows...)
		list.notice, list.fullLabels = notice, full
		return err
	}
	if value.IsArray() {
		list.rows = append(list.rows, value.Array()...)
		return nil
	}
	data := value.Get("data")
	if data.IsArray() && value.IsObject() && gjson.Valid(value.Raw) {
		seen := make(map[string]bool)
		standard := true
		value.ForEach(func(key, field gjson.Result) bool {
			if seen[key.Str] {
				standard = false
				return false
			}
			seen[key.Str] = true
			switch key.Str {
			case "data":
				standard = field.IsArray()
			case "object":
				standard = field.Type == gjson.String && (field.Str == "list" || field.Str == "page")
			case "has_more":
				// A single-response action cannot advance a continuation. Keep
				// that metadata visible rather than presenting a complete list.
				standard = field.Type == gjson.False
			case "first_id", "last_id":
				standard = field.Type == gjson.String || field.Type == gjson.Null
			default:
				standard = false
			}
			return standard
		})
		if standard {
			list.rows = append(list.rows, data.Array()...)
			return nil
		}
		list.fullLabels = true
		list.notice = "Showing the complete response."
	}
	list.rows = append(list.rows, value)
	return nil
}
