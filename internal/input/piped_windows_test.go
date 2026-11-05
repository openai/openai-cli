//go:build windows

package input

import (
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func TestWindowsPipedDataAvailable(t *testing.T) {
	for _, delay := range []time.Duration{0, 2 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				const payload = "synthetic piped request"
				done := make(chan error, 1)
				go func() {
					time.Sleep(delay)
					_, err := io.WriteString(writer, payload)
					writer.Close()
					done <- err
				}()
				available := pipedDataAvailableWindows(reader.Fd())
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if !available {
					t.Fatal("missed piped data during startup")
				}
				data, err := io.ReadAll(reader)
				if err != nil || string(data) != payload {
					t.Fatalf("probe consumed or changed bytes: %q, %v", data, err)
				}
			})
		})
	}
	t.Run("empty open pipe remains bounded", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			// End a broken unbounded probe without hanging the test process.
			stop := time.AfterFunc(50*time.Millisecond, func() { writer.Close() })
			defer stop.Stop()
			start := time.Now()
			if pipedDataAvailableWindows(reader.Fd()) {
				t.Fatal("reported data in an empty pipe")
			}
			if time.Since(start) >= 50*time.Millisecond {
				t.Fatal("probe waited for the writer to close")
			}
		})
	})
	t.Run("closed pipe returns immediately", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			writer.Close()
			start := time.Now()
			if pipedDataAvailableWindows(reader.Fd()) || time.Since(start) != 0 {
				t.Fatal("closed pipe should return false without waiting")
			}
		})
	})
}
