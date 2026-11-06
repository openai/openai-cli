package app

import (
	"bytes"
	"encoding/json"
	"io"
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
}

func (page *listPageContinuation) Read(p []byte) (int, error) {
	n, err := page.ReadCloser.Read(p)
	page.observe(p[:n])
	return n, err
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
