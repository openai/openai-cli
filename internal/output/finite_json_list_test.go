package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
)

type finiteListRecord string

func (r finiteListRecord) RawJSON() string { return string(r) }

type finiteListTestIterator struct {
	items        []finiteListRecord
	index, calls int
	err          error
	next         func(int)
}

func (it *finiteListTestIterator) Next() bool {
	it.calls++
	if it.next != nil {
		it.next(it.calls)
	}
	if it.index == len(it.items) {
		return false
	}
	it.index++
	return true
}
func (it *finiteListTestIterator) Current() finiteListRecord { return it.items[it.index-1] }
func (it *finiteListTestIterator) Err() error                { return it.err }

func finiteListOutputFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	t.Cleanup(func() { file.Close() })
	return file
}
func finiteListReadOutput(t *testing.T, file *os.File) string {
	t.Helper()
	data, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	return string(data)
}

func TestFiniteJSONListTerminalPrefixKeepsFramingAndFailure(t *testing.T) {
	for _, height := range []int{4, 100} {
		for _, failure := range []error{nil, errors.New("synthetic later page")} {
			out := finiteListOutputFile(t)
			previous := os.Stdout
			os.Stdout = out
			func() {
				defer func() { os.Stdout = previous }()
				source := &finiteListTestIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}
				source.next = func(n int) {
					if n == 2 && failure != nil {
						source.err = failure
						source.items = source.items[:1]
					}
				}
				opts := FiniteJSONListOptions{IteratorOptions: IteratorOptions{Context: t.Context()}, Stdout: out}
				iter := NewIterator(source, IteratorOptions{
					Context:   opts.Context,
					Transform: transformers.Identity,
					Limit:     -1,
				})
				list := &finiteJSONList{iter: iter, opts: opts}
				err := list.showTerminal(80, height)
				if failure != nil {
					require.ErrorIs(t, err, failure)
					require.Contains(t, finiteListReadOutput(t, out), `"one"`)
					require.False(t, json.Valid([]byte(finiteListReadOutput(t, out))))
				} else {
					require.NoError(t, err)
					require.JSONEq(t, `[{"id":"one"},{"id":"two"}]`, finiteListReadOutput(t, out))
				}
			}()
		}
	}
}

// Destination classification is supplied by assembly; a wrapped terminal need
// not be an os.File, and FORCE_COLOR alone must not color a pipe.
func TestFiniteJSONListFinalDestinationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name           string
		terminal       bool
		force, noColor string
		wantColor      bool
	}{
		{name: "wrapped terminal", terminal: true, wantColor: true},
		{name: "terminal disabled", terminal: true, force: "0"},
		{name: "NO_COLOR wins", terminal: true, force: "1", noColor: "1"},
		{name: "pipe forced", force: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORCE_COLOR", tc.force)
			t.Setenv("NO_COLOR", tc.noColor)
			var out bytes.Buffer
			source := &finiteListTestIterator{items: []finiteListRecord{`{"value":"\u001b]52;c;synthetic\u0007"}`}}
			err := WriteFiniteJSONList(source, FiniteJSONListOptions{
				IteratorOptions: IteratorOptions{Context: t.Context(), Transform: transformers.Identity, Limit: -1},
				Stdout:          &out, Terminal: tc.terminal,
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantColor, bytes.Contains(out.Bytes(), []byte("\x1b[")))
			require.NotContains(t, out.String(), "\x1b]52")
			require.NotContains(t, out.String(), "\a")
			if !tc.wantColor {
				require.JSONEq(t, `[{"value":"\u001b]52;c;synthetic\u0007"}]`, out.String())
			}
		})
	}
}
