package output

import (
	"context"
	"errors"
	"io"
	"testing"
)

type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }

func TestWriterPreservesCancellationAndSinkFailures(t *testing.T) {
	failure := errors.New("synthetic sink failure")
	for _, method := range []string{"bytes", "string"} {
		for _, tc := range []struct {
			name                  string
			before, during, short bool
			sinkError, wantError  error
			wantCount             int
			wantSinkError         bool
		}{
			{name: "success", wantCount: 4},
			{name: "cancel before", before: true, wantError: context.Canceled},
			{name: "cancel during", during: true, wantCount: 4, wantError: context.Canceled},
			{name: "short write", short: true, wantCount: 3, wantError: io.ErrShortWrite, wantSinkError: true},
			{name: "sink failure", sinkError: failure, wantError: failure, wantSinkError: true},
			{name: "sink failure while canceled", during: true, sinkError: failure, wantError: failure, wantSinkError: true},
			{name: "short write while canceled", during: true, short: true, wantCount: 3, wantError: io.ErrShortWrite, wantSinkError: true},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.before {
					cancel()
				}
				calls := 0
				writer := Writer{Context: ctx, Sink: writeFunc(func(p []byte) (int, error) {
					calls++
					if string(p) != "data" {
						t.Fatalf("changed output: %q", p)
					}
					if tc.during {
						cancel()
					}
					if tc.sinkError != nil {
						return 0, tc.sinkError
					}
					if tc.short {
						return len(p) - 1, nil
					}
					return len(p), nil
				})}
				var n int
				var err error
				if method == "bytes" {
					n, err = writer.Write([]byte("data"))
				} else {
					n, err = writer.WriteString("data")
				}
				var sinkError *WriteError
				if n != tc.wantCount || !errors.Is(err, tc.wantError) || errors.As(err, &sinkError) != tc.wantSinkError {
					t.Fatalf("write = %d, %v (sink: %v); want %d, %v (sink: %v)", n, err, sinkError != nil, tc.wantCount, tc.wantError, tc.wantSinkError)
				}
				if tc.before && calls != 0 || !tc.before && calls != 1 {
					t.Fatalf("sink calls = %d; canceled-before = %v", calls, tc.before)
				}
			})
		}
	}
}
