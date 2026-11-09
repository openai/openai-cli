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
type imagePickerNativeTransfer struct {
	context      context.Context
	cancel       context.CancelFunc
	replace      chan struct{}
	done         chan struct{}
	err          error
	replaced     bool
	index        int
	inlineColumn int // Zero for the Konsole alternate screen.
	expanded     bool
	scrollOffset int
	size         interactiveImageSize
	keys         []string
	placements   map[int]imagePickerNativeRow
	frame        map[int]imagePickerNativeRow
	ready        map[int]bool
	transient    map[imagePickerNativeRow]string
}

type imagePickerNativeWritten struct{ transfer *imagePickerNativeTransfer }

func (w *imagePickerWorkspace) startNativeTransfer(ctx context.Context, parts, keys []string, placements map[int]imagePickerNativeRow, inlineColumn int) error {
	ctx, cancel := context.WithCancel(ctx)
	transfer := &imagePickerNativeTransfer{
		context: ctx, cancel: cancel, replace: make(chan struct{}, 1), done: make(chan struct{}),
		index: w.selected, size: w.konsoleViewport, keys: keys, placements: placements,
		frame: maps.Clone(w.nativeFrame), ready: maps.Clone(w.nativeReady), transient: maps.Clone(w.nativeTransient),
		inlineColumn: inlineColumn, expanded: w.expanded,
	}
	if inlineColumn != 0 {
		transfer.size = interactiveImageSize{width: w.width, height: w.height}
		transfer.scrollOffset = w.itermScrollOffset()
	}
	w.nativeTransfer = transfer
	out, send := w.out, w.send
	write := func() {
		transfer.replaced, transfer.err = writeImagePickerNativeTransfer(transfer, out, parts)
		close(transfer.done)
	}
	if send == nil {
		write()
		return w.finishNativeTransfer(transfer)
	}
	w.transferWorkers.Add(1)
	go func() {
		defer w.transferWorkers.Done()
		write()
		send(imagePickerNativeWritten{transfer})
	}()
	return nil
}

func writeImagePickerNativeTransfer(transfer *imagePickerNativeTransfer, out *os.File, parts []string) (replaced bool, err error) {
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
			if transfer.inlineColumn != 0 {
				// iTerm2 can pause reads while asking permission to show an image.
				// The event loop owns cancellation; replacements yield between jobs.
				length = min(len(part), 16<<10)
				err = terminalimage.WriteControl(transfer.context, out, part[:length])
			} else {
				ctx, cancel := context.WithTimeout(transfer.context, time.Second)
				err = terminalimage.WriteControl(ctx, out, part[:length])
				cancel()
			}
			if err != nil {
				return false, err
			}
			part = part[length:]
		}
	}
	return false, nil
}

func (w *imagePickerWorkspace) finishNativeTransfer(transfer *imagePickerNativeTransfer) error {
	if w.nativeTransfer != transfer {
		return nil
	}
	<-transfer.done
	w.nativeTransfer = nil
	canceled := transfer.context.Err() == context.Canceled
	transfer.cancel()
	err := transfer.err
	if canceled {
		err = withoutInteractiveImageCancellation(err)
	}
	if err != nil || canceled || transfer.replaced {
		w.nativePainted = nil
		if transfer.inlineColumn != 0 {
			// Restore the last saved cursor before rebuilding a partially drawn panel.
			w.nativeColumn = 0
		} else {
			w.painted = nil
		}
		return err
	}
	w.painted, w.nativePainted = transfer.keys, transfer.placements
	if transfer.inlineColumn != 0 {
		w.nativeColumn = transfer.inlineColumn
	} else {
		w.started, w.origin, w.originKnown = true, 0, true
	}
	frame, ready, transient := w.nativeFrame, w.nativeReady, w.nativeTransient
	w.nativeFrame, w.nativeReady, w.nativeTransient = transfer.frame, transfer.ready, transfer.transient
	w.retainNativeFrame()
	w.nativeFrame, w.nativeReady, w.nativeTransient = frame, ready, transient
	return nil
}

func (w *imagePickerWorkspace) stopNativeTransfer() error {
	transfer := w.nativeTransfer
	if transfer == nil {
		return nil
	}
	transfer.cancel()
	return imagePickerDisplayFailure(w.finishNativeTransfer(transfer))
}
