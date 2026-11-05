package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type kittyViewWriter struct {
	io.Writer
	limit int
}

func (w kittyViewWriter) kittyFrameSize() int { return w.limit }

func TestKittyViewPreservesPreparedRowsAndText(t *testing.T) {
	var prepared bytes.Buffer
	require.NoError(t, writePreviewStripe(t.Context(), &prepared, testImage(t), "kitty", 50))
	var original kittyTransfers
	original.accept(t, prepared.Bytes())
	require.Len(t, original.completed, 1)
	before := "\x1b7\r\x1b[J" + strings.Repeat("日本語🧪", 20) + "\x1b[38;2;12;34;56m"
	between := "\x1b[0m\r\n" + "a" + strings.Repeat("\u0301", 100)
	after := "\x1b8\x1b[?7l\x1b[?25h"
	view := before + prepared.String() + between + prepared.String() + after
	for _, limit := range []int{64, 80} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var transfers kittyTransfers
			var text strings.Builder
			starts, records := 0, 0
			writer := kittyViewWriter{limit: limit, Writer: writerFunc(func(data []byte) (int, error) {
				records++
				require.LessOrEqual(t, len(data), limit)
				require.True(t, utf8.Valid(data), "never split a UTF-8 code point")
				for remaining := string(data); len(remaining) > 0; {
					sequence, _, size, state := ansi.DecodeSequenceWc(remaining, 0, nil)
					require.Positive(t, size)
					require.Equal(t, ansi.NormalState, state, "controls must be complete within each record")
					remaining = remaining[size:]
					if strings.HasPrefix(sequence, "\x1b_G") {
						if strings.Contains(sequence, "a=T") {
							starts++
							require.Contains(t, sequence, "c=50")
							require.Contains(t, sequence, "r=1")
							require.Contains(t, sequence, "C=1")
						}
						transfers.accept(t, []byte(sequence))
					} else {
						text.WriteString(sequence)
					}
				}
				return len(data), nil
			})}
			require.NoError(t, WriteFrame(t.Context(), writer, view, "kitty"))
			require.Greater(t, records, 10)
			require.Equal(t, 2, starts)
			require.Empty(t, transfers.pending)
			require.Equal(t, [][]byte{original.completed[0], original.completed[0]}, transfers.completed)
			require.Equal(t, before+between+after, text.String())
		})
	}
	var ordinary bytes.Buffer
	require.NoError(t, WriteFrame(t.Context(), &ordinary, view, "kitty"))
	require.Equal(t, view, ordinary.String(), "unframed writers keep the original bytes")
}

func TestKittyViewStopsOnInterruptedRecord(t *testing.T) {
	var prepared bytes.Buffer
	require.NoError(t, writePreviewStripe(t.Context(), &prepared, testImage(t), "kitty", 50))
	for _, view := range []string{strings.Repeat("界", 100), prepared.String()} {
		for _, failure := range []string{"error", "short write", "cancel", "cancel and short write"} {
			t.Run(fmt.Sprintf("bytes%d/%s", len(view), failure), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cause := errors.New("synthetic record failure")
				calls := 0
				writer := kittyViewWriter{limit: 64, Writer: writerFunc(func(data []byte) (int, error) {
					calls++
					require.LessOrEqual(t, len(data), 64)
					if strings.Contains(failure, "cancel") {
						cancel()
					}
					if strings.Contains(failure, "short write") {
						return len(data) - 1, nil
					}
					if failure == "error" {
						return 0, cause
					}
					return len(data), nil
				})}
				err := WriteFrame(ctx, writer, view, "kitty")
				if failure == "error" {
					require.ErrorIs(t, err, cause)
				}
				if strings.Contains(failure, "short write") {
					require.ErrorIs(t, err, io.ErrShortWrite)
				}
				if strings.Contains(failure, "cancel") {
					require.ErrorIs(t, err, context.Canceled)
				}
				require.Equal(t, 1, calls, "transport retirement owns subsequent reset writes")
			})
		}
	}
}

func TestKittyViewRejectsUnframeableControls(t *testing.T) {
	for _, view := range []string{
		"\x1b[?",
		"\x1b]8;;" + strings.Repeat("x", 80) + "\x1b\\",
		"\x1b_Gq=2,m=3;" + strings.Repeat("AAAA", 40) + "\x1b\\",
		"\x1b_Gq=2,m=0;" + strings.Repeat("A", 81) + "\x1b\\",
		"\x1b_G" + strings.Repeat("q=2,", 20) + "m=0;AAAA\x1b\\",
	} {
		var output bytes.Buffer
		err := WriteFrame(t.Context(), kittyViewWriter{&output, 64}, view, "kitty")
		require.Error(t, err)
		require.Empty(t, output.Bytes())
	}
}
