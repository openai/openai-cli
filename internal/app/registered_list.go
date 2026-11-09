package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
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
	command.Description = strings.TrimSpace(command.Description + "\n\n" + listview.ControlsHelp)
	if content, ok := command.Metadata["help-content"].(clihelp.Content); ok {
		content.Description = strings.TrimSpace(content.Description + "\n\n" + listview.ControlsHelp)
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
		if cmd.IsSet("max-items") && fmt.Sprint(cmd.Value("max-items")) == "0" {
			return next(ctx, cmd)
		}
		if !InteractiveListOutput(opts) {
			// Generated iterators make their first request before reaching the
			// output writer. Observe it without taking ownership of rendering.
			if opts.Format != "raw" {
				ctx = context.WithValue(ctx, registeredListKey{}, &registeredList{command: cmd})
			}
			return next(ctx, cmd)
		}
		opts.setDefaults()
		return runRegisteredList(opts, cmd, next, listPageMaximum(parent))
	}
	configureListBrowsing(command)
}

type registeredListKey struct{}

// The SDK hides page boundaries behind Next. Yield the records it has already
// decoded immediately before it makes its next request. This keeps cursor and
// retry handling in the SDK, and querying the next page under keyboard control.
type registeredList struct {
	command    *cli.Command
	preparing  bool
	rows       []gjson.Result
	notice     string
	fullLabels bool
	yield      func(listview.Page, error) bool
	start      sync.Once
	stopped    atomic.Bool
	// Local iterators use the same screen capacity without sending an HTTP query.
	pageSizeQuery string
	pageSize      int
	unknownTotal  bool
	// SDK responses can arrive concurrently when an action prepares several lists.
	mu         sync.Mutex
	history    *listCursorHistory
	displayed  *listCursorHistory
	collecting bool
	readers    int
	pending    unboundListPages
}

func runRegisteredList(opts ShowJSONOpts, cmd *cli.Command, action cli.ActionFunc, pageMaximum int) error {
	ctx, cancel := context.WithCancel(opts.Context)
	source := &registeredList{command: cmd, preparing: true}
	flags := pageFlags(cmd)
	var maximum int64
	if flags.hasCap && cmd.IsSet("max-items") {
		switch n := cmd.Value("max-items").(type) {
		case int64:
			maximum = n
		case int:
			maximum = int64(n)
		}
	}
	browse := printedListFrom(opts.Context) == nil
	if browse && flags.size != nil && !cmd.IsSet(flags.size.Names()[0]) {
		source.pageSizeQuery = flags.size.GetQueryPath()
	}
	next, stop := iter.Pull2(source.pages(ctx, func(ctx context.Context) error { return action(ctx, cmd) }))
	defer stop()
	defer cancel()

	// Let the command read request fields, including stdin and named pipes,
	// before installing terminal signal handlers or reading navigation keys.
	if _, err, ok := next(); err != nil || !ok {
		return err
	}
	return runTerminalList(opts, func(opts ShowJSONOpts) error {
		stopCancel := context.AfterFunc(opts.Context, cancel)
		defer stopCancel()
		fromStart := !slices.ContainsFunc(flags.cursors, cmd.IsSet)
		loaded := 0
		view := listViewerOptions(opts)
		return listview.Run(view, func() (listview.Page, error) {
			if browse {
				rows, err := listview.TerminalTableRows(opts.Stdout.(*os.File), view.Hint)
				if err != nil {
					return listview.Page{}, err
				}
				if source.pageSizeQuery != "" && pageMaximum > 0 {
					rows = min(rows, pageMaximum)
				}
				if remaining := maximum - int64(loaded); remaining > 0 && remaining < int64(rows) {
					rows = int(remaining)
				}
				source.mu.Lock()
				source.pageSize = rows
				source.mu.Unlock()
			}
			page, err, _ := next()
			loaded += len(page.Items)
			// A starting cursor, cap, or opaque response can hide the collection size.
			if fromStart && !page.More && err == nil && !source.unknownTotal && page.Notice == "" &&
				(maximum <= 0 || int64(loaded) < maximum) {
				page.Total = loaded
			}
			return page, err
		}, cancel)
	})
}

func (list *registeredList) pages(ctx context.Context, action func(context.Context) error) iter.Seq2[listview.Page, error] {
	return func(yield func(listview.Page, error) bool) {
		list.yield = yield
		err := action(context.WithValue(ctx, registeredListKey{}, list))
		if list.preparing && err != nil {
			yield(listview.Page{}, err)
			return
		}
		if list.beginOutput() {
			yield(listview.Page{Items: list.rows, Notice: list.notice, FullLabels: list.fullLabels}, err)
		}
	}
}

// The first yield hands the terminal to the viewer before HTTP dispatch or
// local iteration. Subsequent yields carry actual pages.
func (list *registeredList) beginOutput() bool {
	list.start.Do(func() {
		if list.preparing {
			list.preparing = false
			list.stopped.Store(!list.yield(listview.Page{}, nil))
		}
	})
	return !list.stopped.Load()
}

func (list *registeredList) yieldPending() bool {
	page := listview.Page{Items: list.rows, More: true}
	list.rows = nil
	list.stopped.Store(!list.yield(page, nil))
	return !list.stopped.Load()
}

// Only the terminal renderer owns page delivery and deferred hints.
func currentRegisteredList(ctx context.Context) *registeredList {
	list := registeredListState(ctx)
	if list != nil && list.yield != nil {
		return list
	}
	return nil
}

func registeredListState(ctx context.Context) *registeredList {
	if ctx == nil {
		return nil
	}
	source, _ := ctx.Value(registeredListKey{}).(*registeredList)
	return source
}

// Webhooks.EventTypes.List returns a single catalog and cannot supply an SDK
// paginator. Other lookups can prepare a real iterator for later consumption.
func webhookEventCatalogRequest(request *http.Request, command *cli.Command) bool {
	const route = "webhook_event_types"
	path := request.URL.EscapedPath()
	if request.Method != http.MethodGet || request.URL.RawQuery != "" || !strings.HasSuffix(path, "/"+route) {
		return false
	}
	var base string
	if command != nil {
		base = command.Root().String("base-url")
	}
	if base == "" {
		base = os.Getenv("OPENAI_BASE_URL")
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	endpoint, err := url.Parse(base)
	if err != nil || request.URL.Scheme != endpoint.Scheme || !strings.EqualFold(request.URL.Host, endpoint.Host) {
		return false
	}
	// The SDK resolves resource paths against the base as a directory.
	if !strings.HasSuffix(endpoint.Path, "/") {
		endpoint.Path += "/"
	}
	return path == endpoint.ResolveReference(&url.URL{Path: route}).EscapedPath()
}

func registeredListRequest(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	source := registeredListState(request.Context())
	if source == nil {
		return next(request)
	}
	if source.yield != nil && !source.beginOutput() {
		return nil, context.Canceled
	}
	if webhookEventCatalogRequest(request, source.command) {
		return next(request)
	}
	// Fixed-size request fingerprints detect cycles of any length without
	// retaining cursors, query parameters, or response records for pipes.
	key := sha256.Sum256([]byte(request.URL.RequestURI()))
	query := request.URL.Query()
	after := query["after"]
	history, bound := request.Context().Value(listCursorKey{source}).(*listCursorHistory)
	source.mu.Lock()
	var readNumber uint64
	if !bound {
		history = source.history
		if history != nil {
			readNumber = history.readNumber
			history.requested = true
		}
	}
	// A forwarding iterator can hide the SDK type from the output writer.
	// Its continuation still forms a boundary. A separate bound preparation
	// has neither the displayed history nor an active Next call.
	displaying := source.collecting && (history == source.displayed ||
		source.displayed == nil && bound && history.reading)
	stalled := history != nil && (history.seen[key] ||
		history.rowIDCursor && len(after) != 0 && after[0] == "")
	source.mu.Unlock()
	if stalled {
		return nil, &listview.PaginationError{}
	}
	if source.yield != nil && displaying && len(source.rows) > 0 && !source.yieldPending() {
		return nil, context.Canceled
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response, err := next(request)
	if err == nil && response != nil && response.Body != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		response.Body = &listPageContinuation{ReadCloser: response.Body, accepted: func(page *listPageContinuation) {
			metadata := &listPageMetadata{requestKey: key, hasMoreField: page.hasMoreField, more: page.more}
			source.mu.Lock()
			defer source.mu.Unlock()
			if bound {
				history.page = metadata
			} else if history != nil && source.history == history && readNumber == history.readNumber {
				source.pending.acceptDuringRead(history, metadata)
			} else if source.readers == 0 || history != nil && (!history.reading || readNumber != history.readNumber) {
				// A synchronous SDK continuation cannot finish after its Next returned.
				source.pending.add(metadata)
			}
		}}
	}
	return response, err
}

// Apply an automatic size only to requests using the owning list's flags,
// after the client's page boundary observer has yielded and fingerprinted the URL.
func boundListRequest(command *cli.Command) option.Middleware {
	return func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		source := registeredListState(request.Context())
		if source != nil && source.command == command && source.pageSizeQuery != "" && request.Method == http.MethodGet {
			query := request.URL.Query()
			if !query.Has(source.pageSizeQuery) {
				request = request.Clone(request.Context())
				source.mu.Lock()
				size := source.pageSize
				source.mu.Unlock()
				query.Set(source.pageSizeQuery, strconv.Itoa(size))
				request.URL.RawQuery = query.Encode()
			}
		}
		return next(request)
	}
}

// Local iterators have no HTTP boundary. Backpressure keeps even an unlimited
// producer responsive and lets quit stop it after at most one lookahead item.
const localListPageSize = 32

func collectListIterator[T any](list *registeredList, source jsonview.Iterator[T], maximum int64, opts ShowJSONOpts) error {
	if !list.beginOutput() {
		return context.Canceled
	}
	if maximum == 0 {
		return source.Err()
	}
	rows := clioutput.NewIterator(source, clioutput.IteratorOptions{
		Context: opts.Context, Transform: transformers.Identity, Limit: maximum,
	})
	list.mu.Lock()
	previous, wasCollecting := list.displayed, list.collecting
	list.displayed, list.collecting = nil, true
	local := true
	if pager, ok := source.(*listPageIterator[T]); ok {
		list.displayed = pager.history
		local = pager.history.page == nil
	}
	if local && list.pageSize == 0 {
		list.pageSize = localListPageSize
	}
	list.mu.Unlock()
	defer func() {
		list.mu.Lock()
		list.displayed, list.collecting = previous, wasCollecting
		list.mu.Unlock()
	}()
	for rows.Next() {
		if local && len(list.rows) >= list.pageSize && !list.yieldPending() {
			return context.Canceled
		}
		list.rows = append(list.rows, rows.Current().Result)
	}
	if rows.LimitReached() {
		list.unknownTotal = true
	}
	return rows.Err()
}

func (list *registeredList) collectResponse(value gjson.Result, opts ShowJSONOpts) error {
	if !list.beginOutput() {
		return context.Canceled
	}
	if opts.Operation == "(resource) webhooks.event_types > (method) list" {
		rows, notice, full, err := transformers.WebhookEventTypeRows(opts.Context, value)
		list.rows = append(list.rows, rows...)
		list.notice, list.fullLabels = notice, full
		list.unknownTotal = list.unknownTotal || full
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
	list.unknownTotal = true
	list.rows = append(list.rows, value)
	return nil
}
