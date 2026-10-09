package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestDisplaySavedImagesSeparatesRenderedBatchEntries(t *testing.T) {
	for _, test := range []struct {
		name     string
		rendered []bool
		want     string
	}{
		{"empty", nil, ""},
		{"single", []bool{true}, "image-1\n"},
		{"two", []bool{true, true}, "image-1\n\nimage-2\n"},
		{"three", []bool{true, true, true}, "image-1\n\nimage-2\n\nimage-3\n"},
		{"all skipped", []bool{false, false, false}, ""},
		{"skipped then rendered", []bool{false, true}, "image-2\n"},
		{"rendered then skipped", []bool{true, false}, "image-1\n\n"},
		{"mixed", []bool{false, true, false, true}, "image-2\n\nimage-4\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			saved := batchPreviewImages(len(test.rendered))
			var out bytes.Buffer
			calls := 0
			err := displaySavedImages(t.Context(), saved, &out, func(file imageoutput.SavedImage) (bool, error) {
				require.Equal(t, saved[calls], file)
				rendered := test.rendered[calls]
				calls++
				if !rendered {
					return false, nil
				}
				_, err := fmt.Fprintln(&out, file.Path)
				return err == nil, err
			})
			require.NoError(t, err)
			require.Equal(t, len(saved), calls)
			require.Equal(t, test.want, out.String())
		})
	}
}

func TestDisplaySavedImagesStopsAfterPreviewFailure(t *testing.T) {
	failure := errors.New("synthetic preview failure")
	for _, test := range []struct {
		name     string
		failAt   int
		rendered bool
		want     string
	}{
		{"first preview", 0, false, ""},
		{"later preview", 1, false, "image-1\n\n"},
		{"partial preview", 1, true, "image-1\n\npartial image"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			calls := 0
			err := displaySavedImages(t.Context(), batchPreviewImages(3), &out, func(file imageoutput.SavedImage) (bool, error) {
				index := calls
				calls++
				if index == test.failAt {
					if test.rendered {
						_, _ = out.WriteString("partial image")
					}
					return test.rendered, failure
				}
				_, err := fmt.Fprintln(&out, file.Path)
				return err == nil, err
			})
			require.ErrorIs(t, err, failure)
			require.Equal(t, test.failAt+1, calls)
			require.Equal(t, test.want, out.String())
		})
	}
}

func TestDisplaySavedImagesStopsOnCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		before   bool
		rendered bool
		calls    int
		want     string
	}{
		{"before first preview", true, false, 0, ""},
		{"before separator", false, true, 1, "image-1\n"},
		{"after skipped preview", false, false, 1, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.before {
				cancel()
			}
			var out bytes.Buffer
			calls := 0
			err := displaySavedImages(ctx, batchPreviewImages(3), &out, func(file imageoutput.SavedImage) (bool, error) {
				calls++
				if test.rendered {
					_, _ = fmt.Fprintln(&out, file.Path)
				}
				cancel()
				return test.rendered, nil
			})
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, test.calls, calls)
			require.Equal(t, test.want, out.String())
		})
	}
}

func TestDisplaySavedImagesStopsAfterSeparatorFailure(t *testing.T) {
	failure := errors.New("synthetic separator failure")
	for _, test := range []struct {
		name string
		n    int
		err  error
		want error
	}{
		{"failed write", 0, failure, failure},
		{"short write", 0, nil, io.ErrShortWrite},
		{"accepted byte with error", 1, failure, failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := &batchPreviewWriteFailure{n: test.n, err: test.err}
			calls := 0
			err := displaySavedImages(t.Context(), batchPreviewImages(3), out, func(file imageoutput.SavedImage) (bool, error) {
				calls++
				_, err := fmt.Fprintln(out, file.Path)
				return err == nil, err
			})
			require.ErrorIs(t, err, test.want)
			require.Equal(t, 1, calls)
			require.Equal(t, 2, out.writes)
			want := "image-1\n"
			if test.n > 0 {
				want += "\n"
			}
			require.Equal(t, want, out.output.String())
		})
	}
}

func batchPreviewImages(count int) []imageoutput.SavedImage {
	images := make([]imageoutput.SavedImage, count)
	for i := range images {
		images[i].Path = fmt.Sprintf("image-%d", i+1)
		images[i].SHA256[0] = byte(i + 1)
	}
	return images
}

type batchPreviewWriteFailure struct {
	output bytes.Buffer
	writes int
	n      int
	err    error
}

func (w *batchPreviewWriteFailure) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		if w.n > 0 {
			_, _ = w.output.Write(data[:w.n])
		}
		return w.n, w.err
	}
	return w.output.Write(data)
}
