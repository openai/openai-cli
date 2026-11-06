// Package forminput provides value-only text editing and ordered form navigation.
// Callers own key bindings, draft persistence, terminal I/O, and submission.
package forminput

import "unicode"

// Editor edits runes, not bytes. Cursor must be between zero and len(Text).
// The caller owns Text; operations may reuse its backing array.
type Editor struct {
	Text   []rune
	Cursor int
}

func (e *Editor) Insert(value string) {
	r := []rune(value)
	tail := append([]rune(nil), e.Text[e.Cursor:]...)
	e.Text = append(e.Text[:e.Cursor], r...)
	e.Text = append(e.Text, tail...)
	e.Cursor += len(r)
}
func (e *Editor) Backspace() {
	if e.Cursor > 0 {
		e.Text = append(e.Text[:e.Cursor-1], e.Text[e.Cursor:]...)
		e.Cursor--
	}
}
func (e *Editor) Delete() {
	if e.Cursor < len(e.Text) {
		e.Text = append(e.Text[:e.Cursor], e.Text[e.Cursor+1:]...)
	}
}
func (e *Editor) Move(delta int) { e.Cursor = max(0, min(len(e.Text), e.Cursor+delta)) }
func (e *Editor) LineStart() {
	for e.Cursor > 0 && e.Text[e.Cursor-1] != '\n' {
		e.Cursor--
	}
}
func (e *Editor) LineEnd() {
	for e.Cursor < len(e.Text) && e.Text[e.Cursor] != '\n' {
		e.Cursor++
	}
}
func (e *Editor) DeleteBefore() { e.Text, e.Cursor = e.Text[e.Cursor:], 0 }
func (e *Editor) Clear()        { e.Text, e.Cursor = nil, 0 }
func (e *Editor) DeleteWordBefore() {
	start := e.Cursor
	for start > 0 && unicode.IsSpace(e.Text[start-1]) {
		start--
	}
	for start > 0 && !unicode.IsSpace(e.Text[start-1]) {
		start--
	}
	e.Text = append(e.Text[:start], e.Text[e.Cursor:]...)
	e.Cursor = start
}
func (e *Editor) MoveRow(down bool) {
	start := e.Cursor
	for start > 0 && e.Text[start-1] != '\n' {
		start--
	}
	column := e.Cursor - start
	if down {
		end := e.Cursor
		for end < len(e.Text) && e.Text[end] != '\n' {
			end++
		}
		if end == len(e.Text) {
			return
		}
		start = end + 1
	} else {
		if start == 0 {
			return
		}
		start--
		for start > 0 && e.Text[start-1] != '\n' {
			start--
		}
	}
	end := start
	for end < len(e.Text) && e.Text[end] != '\n' {
		end++
	}
	e.Cursor = min(end, start+column)
}

// Cycle wraps through visible focus IDs. Missing focus and empty orders preserve
// current and return false, letting each caller choose its recovery policy.
func Cycle[T comparable](order []T, current T, backwards bool) (T, bool) {
	for i, id := range order {
		if id == current {
			delta := 1
			if backwards {
				delta = -1
			}
			return order[(i+len(order)+delta)%len(order)], true
		}
	}
	return current, false
}
