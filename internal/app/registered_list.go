package app

import (
	"context"
	"iter"
	"net/http"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-go/v3/option"
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

const listControlsHelp = "Space shows the next screen. Use the up and down arrows to scroll one row. Press b to go back or q to quit. The footer shows the visible range."

// Apply the same terminal behavior after the command tree and its aliases exist.
func configureRegisteredLists(root *cli.Command) {
	var visit func(*cli.Command, string)
	visit = func(parent *cli.Command, parentPath string) {
		for _, child := range parent.Commands {
			path := strings.TrimSpace(parentPath + " " + strings.ReplaceAll(child.Name, ":", " "))
			for _, name := range child.Names() {
				if name == "ls" {
					name = "list"
				}
				if name == "list" || strings.HasPrefix(name, "list-") {
					registerListPager(child, parentPath, path, name)
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
	help := "Automatic terminal output uses a pager. " + listControlsHelp
	command.Description = strings.TrimSpace(command.Description + "\n\n" + help)
	if content, ok := command.Metadata["help-content"].(clihelp.Content); ok {
		content.Description = strings.TrimSpace(content.Description + "\n\n" + help)
		command.Metadata["help-content"] = content
	}

	resource := strings.ReplaceAll(strings.ReplaceAll(parent, " ", "."), "-", "_")
	operation := "(resource) " + resource + " > (method) " + strings.ReplaceAll(method, "-", "_")
	next := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("watch") || cmd.Bool("stream") {
			return next(ctx, cmd)
		}
		opts := ShowJSONOpts{
			Context: ctx, Title: title, Operation: operation, OutputKind: OutputPageItem,
			Format: cmd.Root().String("format"), Transform: cmd.Root().String("transform"), RawOutput: cmd.Root().Bool("raw-output"),
		}
		if !InteractiveListOutput(opts) || cmd.IsSet("max-items") && cmd.Value("max-items") == int64(0) {
			return next(ctx, cmd)
		}
		opts.setDefaults()
		return runInteractiveList(opts, func(opts ShowJSONOpts) error {
			return runRegisteredList(opts, func(ctx context.Context) error { return next(ctx, cmd) })
		})
	}
}

type registeredListKey struct{}

// The SDK hides page boundaries behind Next. Yield the records it has already
// decoded immediately before it makes its next request. This keeps cursor and
// retry handling in the SDK, and querying the next page under keyboard control.
type registeredList struct {
	rows       []gjson.Result
	notice     string
	fullLabels bool
	yield      func(listview.Page, error) bool
	stopped    bool
	seen       map[string]bool
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
		if source.seen[key] {
			return nil, &listview.PaginationError{}
		}
		if !source.yieldPending() {
			return nil, context.Canceled
		}
	}
	response, err := next(request)
	if err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		source.seen[key] = true
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
	rows := clioutput.NewIterator(source, clioutput.IteratorOptions{
		Context: opts.Context, Transform: transformers.Identity, Limit: maximum,
	})
	for rows.Next() {
		if len(list.seen) == 0 && len(list.rows) == localListPageSize && !list.yieldPending() {
			return context.Canceled
		}
		list.rows = append(list.rows, rows.Current().Result)
	}
	return rows.Err()
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
