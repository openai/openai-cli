package images

import (
	"context"
	"errors"
	"maps"
	"os"
	"time"

	"github.com/openai/openai-cli/internal/terminalimage"
)

// One transfer owns the terminal until its PNG completes or a bounded reset succeeds.
// Ordinary replacement stops between helper jobs, preserving the reusable worker.
type imagePickerKonsoleTransfer struct {
	context    context.Context
	cancel     context.CancelFunc
	replace    chan struct{}
	done       chan struct{}
	err        error
	replaced   bool
	index      int
	size       interactiveImageSize
	keys       []string
	placements map[int]imagePickerNativeRow
	frame      map[int]imagePickerNativeRow
	ready      map[int]bool
	transient  map[imagePickerNativeRow]string
}

type imagePickerKonsoleWritten struct{ transfer *imagePickerKonsoleTransfer }

func (w *imagePickerWorkspace) startKonsoleTransfer(ctx context.Context, parts, keys []string, placements map[int]imagePickerNativeRow) error {
	ctx, cancel := context.WithCancel(ctx)
	transfer := &imagePickerKonsoleTransfer{
		context: ctx, cancel: cancel, replace: make(chan struct{}, 1), done: make(chan struct{}),
		index: w.selected, size: w.konsoleViewport, keys: keys, placements: placements,
		frame: maps.Clone(w.nativeFrame), ready: maps.Clone(w.nativeReady), transient: maps.Clone(w.nativeTransient),
	}
	w.konsoleTransfer = transfer
	out, send := w.out, w.send
	write := func() {
		transfer.replaced, transfer.err = writeImagePickerKonsoleTransfer(transfer, out, parts)
		close(transfer.done)
	}
	if send == nil {
		write()
		return w.finishKonsoleTransfer(transfer)
	}
	w.konsoleWorkers.Add(1)
	go func() {
		defer w.konsoleWorkers.Done()
		write()
		send(imagePickerKonsoleWritten{transfer})
	}()
	return nil
}

func writeImagePickerKonsoleTransfer(transfer *imagePickerKonsoleTransfer, out *os.File, parts []string) (replaced bool, err error) {
	defer func() {
		if err == nil && !replaced {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(transfer.context), 250*time.Millisecond)
		defer cancel()
		err = errors.Join(err, terminalimage.WriteControl(ctx, out, "\x00\x1b\\"))
	}()
	for _, part := range parts {
		for len(part) > 0 {
			if err := transfer.context.Err(); err != nil {
				return false, err
			}
			select {
			case <-transfer.replace:
				return true, nil
			default:
			}
			length := min(len(part), 256<<10)
			ctx, cancel := context.WithTimeout(transfer.context, time.Second)
			err := terminalimage.WriteControl(ctx, out, part[:length])
			cancel()
			if err != nil {
				return false, err
			}
			part = part[length:]
		}
	}
	return false, nil
}

func (w *imagePickerWorkspace) finishKonsoleTransfer(transfer *imagePickerKonsoleTransfer) error {
	if w.konsoleTransfer != transfer {
		return nil
	}
	<-transfer.done
	w.konsoleTransfer = nil
	canceled := transfer.context.Err() == context.Canceled
	transfer.cancel()
	err := transfer.err
	if canceled {
		err = withoutInteractiveImageCancellation(err)
	}
	if err != nil || canceled || transfer.replaced {
		w.painted, w.nativePainted = nil, nil
		return err
	}
	w.painted, w.nativePainted = transfer.keys, transfer.placements
	w.started, w.origin, w.originKnown = true, 0, true
	frame, ready, transient := w.nativeFrame, w.nativeReady, w.nativeTransient
	w.nativeFrame, w.nativeReady, w.nativeTransient = transfer.frame, transfer.ready, transfer.transient
	w.retainNativeFrame()
	w.nativeFrame, w.nativeReady, w.nativeTransient = frame, ready, transient
	return nil
}

func (w *imagePickerWorkspace) stopKonsoleTransfer() error {
	transfer := w.konsoleTransfer
	if transfer == nil {
		return nil
	}
	transfer.cancel()
	return imagePickerDisplayFailure(w.finishKonsoleTransfer(transfer))
}
