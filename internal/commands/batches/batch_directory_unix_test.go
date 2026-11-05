//go:build !windows

package batches

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchDirectoryStartupTimeoutDoesNotGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	startup := time.AfterFunc(0, func() {
		close(started)
		<-release
		cancel()
		close(finished)
	})
	defer func() { close(release); <-finished }()
	<-started
	// The timer has fired, but its callback has not canceled the context.
	require.NoError(t, ctx.Err())
	var grant bytes.Buffer
	err := grantBatchDirectoryOperation(ctx, startup, bytes.NewReader([]byte{'R'}), &grant)
	require.ErrorContains(t, err, "did not become ready")
	require.Empty(t, grant.Bytes(), "startup timeout must prevent filesystem permission")
	require.NoError(t, ctx.Err(), "fixture must keep callback cancellation pending")
}

func TestBatchDirectoryStartupGrant(t *testing.T) {
	for _, name := range []string{"ready", "missing_ready", "bad_ready", "canceled", "closed_lifeline"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fired := make(chan struct{})
			startup := time.AfterFunc(time.Hour, func() { close(fired) })
			defer startup.Stop()
			ready := []byte{'R'}
			var grant bytes.Buffer
			var life io.Writer = &grant
			switch name {
			case "missing_ready":
				ready = nil
			case "bad_ready":
				ready[0] = 'X'
			case "canceled":
				cancel()
			case "closed_lifeline":
				reader, writer := io.Pipe()
				require.NoError(t, reader.Close())
				defer writer.Close()
				life = writer
			}
			err := grantBatchDirectoryOperation(ctx, startup, bytes.NewReader(ready), life)
			if name == "ready" {
				require.NoError(t, err)
				require.Equal(t, "G", grant.String())
			} else {
				require.Error(t, err)
				require.Empty(t, grant.Bytes())
			}
			require.False(t, startup.Stop(), "handshake must stop its startup timer")
			select {
			case <-fired:
				t.Fatal("startup timer fired after handshake")
			default:
			}
		})
	}
}
