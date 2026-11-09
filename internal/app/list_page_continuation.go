package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-go/v3/packages/pagination"
)

// The SDK owns decoding the page and selecting its cursor. Its iterator can
// nevertheless return EOF when the server explicitly promised another page.
// Observe only the top-level has_more boolean as the SDK reads the original body.
// The bounded name holds even the fully Unicode-escaped spelling, with quotes.
type listPageContinuation struct {
	io.ReadCloser
	depth                 int
	quoted, escaped       bool
	matchedKey, readValue bool
	key                   [2 + 6*len("has_more") + 1]byte
	keyLen                int
	hasMoreField, more    bool
	read                  bool
	accepted              func(*listPageContinuation)
}

func (page *listPageContinuation) Read(p []byte) (int, error) {
	n, err := page.ReadCloser.Read(p)
	if n > 0 {
		page.read = true
		page.observe(p[:n])
	}
	return n, err
}

func (page *listPageContinuation) Close() error {
	// The SDK closes retry responses without reading them. Only a body it read
	// establishes a page. Keep metadata independently of the response body.
	if page.read && page.accepted != nil {
		page.accepted(page)
		page.accepted = nil
	}
	return page.ReadCloser.Close()
}

func (page *listPageContinuation) observe(data []byte) {
	for i := 0; i < len(data); i++ {
		if page.quoted {
			// Record strings can be large. Skip their contents without keeping
			// a copy, while respecting escapes and closing quotes across reads.
			if !page.escaped && (page.depth != 1 || page.keyLen == len(page.key)) {
				offset := bytes.IndexAny(data[i:], `"\`)
				if offset < 0 {
					return
				}
				i += offset
			}
			c := data[i]
			if page.depth == 1 && page.keyLen < len(page.key) {
				page.key[page.keyLen] = c
				page.keyLen++
			}
			switch {
			case page.escaped:
				page.escaped = false
			case c == '\\':
				page.escaped = true
			case c == '"':
				page.quoted = false
				page.matchedKey = page.depth == 1 && page.isContinuationKey()
			}
			continue
		}
		c := data[i]
		if c <= ' ' {
			continue
		}
		if page.matchedKey && c == ':' {
			page.matchedKey, page.readValue = false, true
			continue
		}
		page.matchedKey = false
		if page.readValue {
			page.hasMoreField = true
			page.more = c == 't'
			page.readValue = false
		}
		switch c {
		case '{', '[':
			page.depth++
		case '}', ']':
			page.depth--
		case '"':
			page.quoted = true
			page.keyLen = 1
			page.key[0] = '"'
		}
	}
}

func (page *listPageContinuation) isContinuationKey() bool {
	if page.keyLen == len(page.key) {
		return false
	}
	name := page.key[:page.keyLen]
	if !bytes.ContainsRune(name, '\\') {
		return string(name) == `"has_more"`
	}
	var decoded string
	return json.Unmarshal(name, &decoded) == nil && decoded == "has_more"
}

type listPageMetadata struct {
	requestKey                   [sha256.Size]byte
	hasMoreField, more, hadItems bool
}

type listCursorHistory struct {
	page               *listPageMetadata
	seen               map[[sha256.Size]byte]bool
	rowIDCursor        bool
	reading            bool
	overlap            bool
	readNumber         uint64
	readKey            [sha256.Size]byte
	requested          bool
	accepted           bool
	unboundMore        bool
	unboundMissingFlag bool
}

type listCursorKey struct{ list *registeredList }

func listSourceOwnershipError() error {
	return &localcommands.LocalError{Message: "Cannot verify which prepared list owns the continuation. Use PrepareListIterator for each SDK pager. Output may be incomplete."}
}

// Generated actions prepare one immediate pager. If several responses arrive
// without a binding, retain only the possible EOF conditions, never their order.
type unboundListPages struct {
	count       int
	single      *listPageMetadata
	more        bool
	missingFlag bool
}

func (pages *unboundListPages) add(page *listPageMetadata) {
	pages.count++
	if pages.count == 1 {
		pages.single = page
	} else {
		pages.single = nil
	}
	pages.more = pages.more || page.more
	pages.missingFlag = pages.missingFlag || !page.hasMoreField
}

func (pages *unboundListPages) claim(history *listCursorHistory) {
	history.page = pages.single
	history.unboundMore = pages.more
	history.unboundMissingFlag = pages.missingFlag
	if pages.count <= 1 {
		*pages = unboundListPages{}
	} else {
		pages.count--
	}
}

// An SDK Next accepts at most one response; retries discard their bodies.
// Additional successful responses can belong to separately prepared pagers.
func (pages *unboundListPages) acceptDuringRead(history *listCursorHistory, page *listPageMetadata) {
	if !history.accepted {
		history.accepted = true
		history.page = page
		return
	}
	if first := history.page; first != nil {
		history.unboundMore = history.unboundMore || first.more
		history.unboundMissingFlag = history.unboundMissingFlag || !first.hasMoreField
	}
	history.unboundMore = history.unboundMore || page.more
	history.unboundMissingFlag = history.unboundMissingFlag || !page.hasMoreField
	history.page = nil
	pages.count++
	pages.single = nil
	pages.more = pages.more || history.unboundMore
	pages.missingFlag = pages.missingFlag || history.unboundMissingFlag
}

// PrepareListIterator binds one SDK pager to its HTTP requests before
// ListAutoPaging fetches the first page. Compound actions use a separate call
// for each pager and the callback's context only for that pager's constructor.
func PrepareListIterator[T any, I jsonview.Iterator[T]](ctx context.Context, prepare func(context.Context) I) jsonview.Iterator[T] {
	list := registeredListState(ctx)
	if list == nil {
		return prepare(ctx)
	}
	history := new(listCursorHistory)
	source := prepare(context.WithValue(ctx, listCursorKey{list}, history))
	return checkListPagination[T](source, list, history)
}

// SDK pagers can report EOF despite a promised continuation. Check EOF in
// Next so every output writer sees the failure before finalizing its output.
// Writers that stop at an item limit never ask for that next item.
type listPageIterator[T any] struct {
	jsonview.Iterator[T]
	list       *registeredList
	usesLastID bool
	history    *listCursorHistory
	bound      bool
	hadItems   bool
	err        error
}

// This unnamed struct can only forward the embedded methods. Named decorators
// can override them and must receive the checked pager from PrepareListIterator.
type forwardedListIterator[T any] = struct{ jsonview.Iterator[T] }

func forwardedListSource[T any](source jsonview.Iterator[T]) jsonview.Iterator[T] {
	var pointers map[*forwardedListIterator[T]]bool
	for {
		switch wrapped := source.(type) {
		case forwardedListIterator[T]:
			source = wrapped.Iterator
		case *forwardedListIterator[T]:
			if wrapped == nil || pointers[wrapped] {
				return nil
			}
			if pointers == nil {
				pointers = make(map[*forwardedListIterator[T]]bool)
			}
			pointers[wrapped] = true
			source = wrapped.Iterator
		default:
			return source
		}
	}
}

func checkListPagination[T any](source jsonview.Iterator[T], list *registeredList, history *listCursorHistory) jsonview.Iterator[T] {
	underlying := forwardedListSource(source)
	if underlying == nil {
		return &listPageIterator[T]{list: list, history: new(listCursorHistory),
			err: &localcommands.LocalError{Message: "The list iterator has an empty or cyclic forwarding source."}}
	}
	checked := &listPageIterator[T]{Iterator: underlying, list: list, history: history, bound: history != nil}
	switch underlying.(type) {
	case *listPageIterator[T]:
		return underlying
	case *pagination.PageAutoPager[T]:
		return source
	case *pagination.ConversationCursorPageAutoPager[T]:
		checked.usesLastID = true
	case *pagination.CursorPageAutoPager[T], *pagination.NextCursorPageAutoPager[T], *pagination.TokenPageAutoPager[T]:
	default:
		if !checked.bound {
			return source
		}
	}
	if !checked.bound {
		checked.history = new(listCursorHistory)
		if underlying.Err() == nil {
			list.mu.Lock()
			list.pending.claim(checked.history)
			list.mu.Unlock()
		}
	}
	_, checked.history.rowIDCursor = underlying.(*pagination.CursorPageAutoPager[T])
	return checked
}

func (it *listPageIterator[T]) Next() bool {
	if it.err != nil {
		return false
	}
	// The SDK issues follow-up requests synchronously inside Next. A separate
	// iterator in the same command, including one with the same URL, starts fresh.
	list := it.list
	list.mu.Lock()
	it.history.reading = true
	if it.bound && it.history.page != nil && list.display != nil && list.display.history == nil {
		// A forwarding iterator may hide the bound pager from ShowJSONIterator.
		list.display.http.Store(true)
	}
	if !it.bound {
		it.history.readNumber++
		it.history.requested, it.history.accepted, it.history.overlap = false, false, false
		list.readers++
		if list.readers == 1 {
			list.history = it.history
		} else {
			// Unbound callers lack a source identity during concurrent reads.
			it.history.overlap = true
			it.history.unboundMore = true
			if list.history != nil {
				list.history.overlap = true
				list.history.unboundMore = true
				list.history.page = nil
			}
			it.history.page = nil
			list.history = nil
		}
	}
	list.mu.Unlock()

	advanced := it.Iterator.Next()

	list.mu.Lock()
	defer list.mu.Unlock()
	it.history.reading = false
	if !it.bound {
		list.readers--
		list.history = nil
	}
	if advanced {
		it.hadItems = true
		// A successful HTTP status alone does not establish that the SDK
		// accepted the page. It may still retry the request.
		if page := it.history.page; page != nil && !page.hadItems {
			page.hadItems = true
			if it.history.seen == nil {
				it.history.seen = make(map[[sha256.Size]byte]bool)
			}
			it.history.seen[page.requestKey] = true
		}
		return true
	}
	page := it.history.page
	if it.Iterator.Err() == nil {
		if page != nil && (page.more || it.usesLastID && !page.hasMoreField && page.hadItems) {
			it.err = &listview.PaginationError{}
		} else if page == nil && !it.bound {
			// A row-ID pager with a row must ask for another page unless the
			// current one is explicitly final. This proves EOF on a serial read.
			provenFinal := it.history.rowIDCursor && it.hadItems && !it.history.requested && !it.history.overlap
			couldContinue := it.history.unboundMore || it.usesLastID && it.hadItems && it.history.unboundMissingFlag
			if it.history.overlap || !provenFinal && couldContinue {
				it.err = listSourceOwnershipError()
			}
		}
	}
	return false
}

func (it *listPageIterator[T]) Err() error {
	if it.err != nil {
		return it.err
	}
	return it.Iterator.Err()
}
