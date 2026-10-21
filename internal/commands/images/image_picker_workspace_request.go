package images

import (
	"context"
	"errors"
	clioutput "github.com/openai/openai-cli/internal/output"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (p *imagePickerInline) startWorkspaceRequest() tea.Cmd {
	result := p.model.result
	result.settings, result.workspace = p.model.settings, p.workspace
	p.selection = &result
	p.model.result = imagePickerResult{}
	p.model.generating, p.busy = true, true
	p.model.note = ""
	p.workspace.animationFrame = 0
	p.workspace.stage, p.workspace.since = clioutput.LoadingWaiting, time.Now()
	ctx, cancel := context.WithCancel(p.requestContext)
	ctx = context.WithValue(ctx, imagePickerWorkspaceKey{}, p.workspace)
	ctx = context.WithValue(ctx, imagePickerLoadingPromptKey{}, result.settings.prompt)
	p.requestCancel, p.requestDone = cancel, make(chan struct{})
	p.requestErr = nil
	done := p.requestDone
	p.draw()
	go func() {
		err := p.generate(ctx, &result)
		if err != nil && imagePickerRecoverableRequest(err) && !cliterminal.IsWriterTerminal(os.Stderr) {
			if displayErr := writeImagePickerRequestMessage(ctx, os.Stderr, p.workspace.root, err, result.draftSaved); displayErr != nil {
				err = displayErr
			}
		}
		p.requestErr = err
		close(done)
		p.workspace.send(imagePickerWorkspaceDone{err, result.draftSaved})
	}()
	return imagePickerWorkspaceClock(p.workspace.since)
}

func (p *imagePickerInline) updateWorkspace(msg tea.Msg) (bool, tea.Cmd) {
	draw := func(header bool) {
		if err := p.workspace.draw(p.model, p.profile, p.busy, header); err != nil {
			p.err = err
			p.output.cancel()
		}
	}
	switch msg := msg.(type) {
	case imagePickerPreviewFailure:
		p.err = msg.err
		p.output.cancel()
		return true, tea.Quit
	case imagePickerOpened:
		p.workspace.opening = false
		p.workspace.openCancel = nil
		p.workspace.openError = msg.err != nil
		p.workspace.openErrorIndex = msg.index
		draw(false)
		return true, nil
	case tea.ModeReportMsg:
		if p.workspace.mouse.report(msg) {
			if p.started {
				draw(false)
			}
			return true, nil
		}
		return false, nil
	case tea.CursorPositionMsg:
		w := p.workspace
		if w.originQueryPending {
			w.originQueryPending = false
			if w.originQueryRevision == w.frameRevision {
				w.origin = msg.Y
				w.originKnown = msg.Y >= 0 && msg.Y+len(w.painted) <= w.height
			}
			draw(false)
		}
		return true, nil
	case imagePickerWorkspaceTick:
		if p.busy && msg.started.Equal(p.workspace.since) {
			p.workspace.animationFrame++
			draw(false)
			return true, imagePickerWorkspaceClock(msg.started)
		}
		return true, nil
	case imagePickerWorkspaceStage:
		p.workspace.stage = clioutput.LoadingStage(msg)
		draw(false)
		return true, nil
	case imagePickerWorkspaceReceipts:
		p.workspace.pendingReceipts = append(p.workspace.pendingReceipts, msg.rows...)
		return p.updateWorkspace(imagePickerWorkspaceRows{msg.rows})
	case imagePickerWorkspaceRows:
		bottom := p.workspace.offset >= max(0, len(p.workspace.displayRows)-max(0, p.workspace.height-p.workspace.top-2))
		p.workspace.rows = append(p.workspace.rows, msg.rows...)
		if bottom && !p.workspace.expanded {
			p.workspace.offset = int(^uint(0) >> 1)
		}
		draw(false)
		return true, nil
	case imagePickerWorkspaceImage:
		w := p.workspace
		bottom := w.offset >= max(0, len(w.displayRows)-max(0, w.height-w.top-2))
		w.appendImage(msg)
		p.model.hasImages = true
		if bottom && !w.expanded {
			w.selected = len(w.images) - 1
			w.offset = int(^uint(0) >> 1)
			w.followSelection = true
		}
		draw(false)
		return true, nil
	case imagePickerExpandedRows:
		w := p.workspace
		if msg.revision == w.previewRevision {
			w.previewPending = false
		}
		if msg.revision == w.previewRevision && msg.index == w.selected {
			w.previewPending = false
			if msg.inline {
				w.replaceImageRows(msg.index, msg.rows, msg.columns, msg.protocol)
				draw(false)
				return true, nil
			}
			w.expandedIndex, w.expandedSize, w.expandedRows = msg.index, msg.size, msg.rows
			if msg.protocol != "" {
				w.images[msg.index].protocol = msg.protocol
			}
			if w.expanded {
				draw(false)
			}
		}
		return true, nil
	case imagePickerWorkspaceDone:
		p.requestCancel()
		p.requestCancel = nil
		p.model.generating, p.busy = false, false
		if msg.draftSaved {
			p.savedSettings = p.model.settings
		}
		p.selection = nil
		if p.model.result.Canceled && imagePickerSafePreviewFailure(msg.err) {
			return true, tea.Quit
		}
		p.submitAfter = time.Now().Add(imagePickerResumeQuiet)
		if msg.err != nil {
			if !imagePickerRecoverableRequest(msg.err) {
				p.err = msg.err
				return true, tea.Quit
			}
			message := imagePickerRequestMessage(p.workspace.root, msg.err, msg.draftSaved)
			bottom := p.workspace.offset >= max(0, len(p.workspace.displayRows)-max(0, p.workspace.height-p.workspace.top-2))
			for _, line := range strings.Split(message, "\n") {
				p.workspace.rows = append(p.workspace.rows, imagePickerLine(line))
			}
			if bottom && !p.workspace.expanded {
				p.workspace.offset = int(^uint(0) >> 1)
			}
			p.model.note = "Request failed. Edit your prompt or settings before trying again."
		} else {
			p.model.note = "All images saved."
		}
		draw(true)
		return true, nil
	case imagePickerStopMsg:
		if p.workspace.previewCancel != nil {
			p.workspace.previewCancel()
		}
		if p.requestCancel != nil {
			p.requestCancel()
		}
	case tea.MouseWheelMsg:
		if p.workspace.expanded {
			if msg.Button == tea.MouseWheelUp {
				p.workspace.scrollExpanded(-3)
			}
			if msg.Button == tea.MouseWheelDown {
				p.workspace.scrollExpanded(3)
			}
			draw(false)
			return true, nil
		}
		y := msg.Y - p.workspace.origin
		if p.workspace.originKnown && y > p.workspace.top && y < len(p.workspace.painted)-1 {
			if msg.Button == tea.MouseWheelUp {
				p.workspace.scroll(-3)
			} else if msg.Button == tea.MouseWheelDown {
				p.workspace.scroll(3)
			}
			draw(false)
		}
		return true, nil
	case tea.KeyPressMsg:
		if !p.workspace.expanded {
			switch msg.String() {
			case "esc", "tab", "shift+tab", "left", "right", "up", "down", "home", "end", "pgup", "pgdown":
				p.workspace.viewAfter = time.Time{}
			}
		}
		if msg.String() == "ctrl+c" {
			if p.workspace.previewCancel != nil {
				p.workspace.previewCancel()
			}
			if p.requestCancel != nil {
				p.requestCancel()
			}
			return false, nil
		}
		if p.workspace.expanded {
			w := p.workspace
			switch msg.String() {
			case "esc", "q", "backspace":
				p.closeExpanded()
			case "left", "right":
				delta := 1
				if msg.String() == "left" {
					delta = -1
				}
				w.selectImage(delta)
				p.prepareExpanded()
			case "up", "down", "pgup", "pgdown", "home", "end":
				delta := 1
				if msg.String() == "pgup" || msg.String() == "pgdown" {
					delta = max(1, p.model.height-4)
				}
				if msg.String() == "up" || msg.String() == "pgup" {
					delta = -delta
				}
				if msg.String() == "home" {
					w.images[w.selected].offset = 0
				} else if msg.String() == "end" {
					w.scrollExpanded(int(^uint(0) >> 1))
				} else {
					w.scrollExpanded(delta)
				}
			}
			draw(false)
			return true, nil
		}
		if p.busy && len(p.workspace.images) > 0 && (msg.String() == "tab" || msg.String() == "shift+tab") {
			if p.model.focus == "images" {
				p.model.focus = "prompt"
			} else {
				p.model.focus = "images"
			}
			draw(true)
			return true, nil
		}
		if p.model.focus == "images" {
			switch msg.String() {
			case "o":
				return true, p.openSavedImage()
			case "enter":
				now := time.Now()
				if now.Before(p.workspace.viewAfter) {
					p.workspace.viewAfter = now.Add(imagePickerResumeQuiet)
					return true, nil
				}
				p.expandImages()
			case "up":
				p.workspace.scroll(-1)
			case "down":
				p.workspace.scroll(1)
			case "left":
				p.workspace.selectImage(-1)
				p.prepareExpanded()
			case "right":
				p.workspace.selectImage(1)
				p.prepareExpanded()
			case "home":
				p.workspace.selectImage(-len(p.workspace.images))
			case "end":
				p.workspace.selectImage(len(p.workspace.images))
			case "esc":
				p.model.focus = "prompt"
			default:
				break
			}
			if msg.String() == "enter" || msg.String() == "left" || msg.String() == "right" || msg.String() == "up" || msg.String() == "down" || msg.String() == "home" || msg.String() == "end" || msg.String() == "esc" {
				p.prepareExpanded()
				draw(true)
				return true, nil
			}
		}
		if (p.busy || p.model.focus != "command") && (msg.String() == "pgup" || msg.String() == "pgdown") {
			delta := max(1, p.workspace.height-p.workspace.top-2)
			if msg.String() == "pgup" {
				delta = -delta
			}
			p.workspace.scroll(delta)
			draw(false)
			return true, nil
		}
		if p.busy {
			return true, nil
		}
	case tea.PasteMsg:
		if p.busy || p.workspace.expanded {
			return true, nil
		}
	}
	return false, nil
}
