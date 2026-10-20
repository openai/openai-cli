package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImagePickerFirstRunBlockedWorkDoesNotBlockCommand(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancellation"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, unblock, foregroundDone, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(unblock) }) })
			workerContext := make(chan context.Context, 1)
			go func() {
				waitForImagePickerFirstRun(ctx, func(ctx context.Context) {
					workerContext <- ctx
					close(started)
					<-unblock // Models an OS operation that ignores context cancellation.
					close(workerDone)
				})
				close(foregroundDone)
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("setup worker did not start")
			}
			if canceled {
				cancel()
			}
			select {
			case <-foregroundDone:
			case <-time.After(2 * time.Second):
				t.Fatal("optional blocked I/O prevented the foreground command from continuing")
			}
			require.Error(t, (<-workerContext).Err())
			secondStarted := make(chan struct{}, 1)
			waitForImagePickerFirstRun(t.Context(), func(context.Context) { secondStarted <- struct{}{} })
			require.Empty(t, secondStarted, "a blocked worker must not accumulate more work")
			release.Do(func() { close(unblock) })
			<-workerDone
			// The worker releases admission after its callback returns.
			require.Eventually(t, func() bool { return len(imagePickerFirstRunWorker) == 0 }, time.Second, time.Millisecond)
			waitForImagePickerFirstRun(t.Context(), func(context.Context) { secondStarted <- struct{}{} })
			require.Len(t, secondStarted, 1, "finished work must release the slot")
		})
	}
}

func TestImagePickerFirstRunAlreadyCanceledDoesNotStartWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := make(chan struct{}, 1)
	waitForImagePickerFirstRun(ctx, func(context.Context) { started <- struct{}{} })
	require.Empty(t, started)
}
