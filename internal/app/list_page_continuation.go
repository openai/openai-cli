package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"

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
	page        *listPageMetadata
	seen        map[[sha256.Size]byte]bool
	rowIDCursor bool
}

// SDK pagers can report EOF despite a promised continuation. Check EOF in
// Next so every output writer sees the failure before finalizing its output.
// Writers that stop at an item limit never ask for that next item.
type listPageIterator[T any] struct {
	jsonview.Iterator[T]
	list       *registeredList
	usesLastID bool
	history    listCursorHistory
	err        error
}

func checkListPagination[T any](source jsonview.Iterator[T], list *registeredList) jsonview.Iterator[T] {
	checked := &listPageIterator[T]{Iterator: source, list: list}
	switch source.(type) {
	case *pagination.CursorPageAutoPager[T]:
		checked.history.rowIDCursor = true
	case *pagination.ConversationCursorPageAutoPager[T]:
		checked.usesLastID = true
	case *pagination.NextCursorPageAutoPager[T], *pagination.TokenPageAutoPager[T]:
	default:
		return source
	}
	if source.Err() == nil {
		list.mu.Lock()
		checked.history.page, list.initial = list.initial, nil
		list.mu.Unlock()
	}
	return checked
}

func (it *listPageIterator[T]) Next() bool {
	// The SDK issues follow-up requests synchronously inside Next. A separate
	// iterator in the same command, including one with the same URL, starts fresh.
	list := it.list
	list.mu.Lock()
	list.readers++
	if list.readers == 1 {
		list.history = &it.history
	} else {
		// Concurrent calls sharing a command context have no unique caller.
		if list.history != nil {
			list.history.page = nil
		}
		it.history.page = nil
		list.history, list.initial, list.ambiguous = nil, nil, true
	}
	list.mu.Unlock()

	advanced := it.Iterator.Next()

	list.mu.Lock()
	defer list.mu.Unlock()
	list.readers--
	list.history = nil
	if advanced {
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
	if page != nil && it.Iterator.Err() == nil &&
		(page.more || it.usesLastID && !page.hasMoreField && page.hadItems) {
		it.err = &listview.PaginationError{}
	}
	return false
}

func (it *listPageIterator[T]) Err() error {
	if it.err != nil {
		return it.err
	}
	return it.Iterator.Err()
}
