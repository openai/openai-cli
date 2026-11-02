package images

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Wheel navigation leaves the keyboard focus and draft caret where they were.
func (p *imagePickerInline) galleryWheel(msg tea.MouseWheelMsg) bool {
	w := p.workspace
	g, wide := imageStudioLayout(w.width, w.height)
	if !w.expanded {
		y := msg.Y - w.origin
		if !w.originKnown || y < w.bodyStart || y >= w.bodyEnd {
			return false
		}
		if wide && (p.model.galleryModal() || msg.X < g.previewX-2 || msg.X >= g.previewX+g.previewWidth) {
			return false
		}
	}
	delta := 1
	switch msg.Button {
	case tea.MouseWheelUp, tea.MouseWheelLeft:
		delta = -1
	case tea.MouseWheelDown, tea.MouseWheelRight:
	default:
		return false
	}
	horizontal := msg.Button == tea.MouseWheelLeft || msg.Button == tea.MouseWheelRight || msg.Mod&tea.ModShift != 0
	if horizontal && (wide || w.expanded) {
		if !w.expanded && (len(w.galleryStarts) == 0 || w.offset+msg.Y-w.origin-w.bodyStart < w.galleryStart(w.selected)) {
			return false
		}
		before := w.selected
		w.selectImage(delta)
		return before != w.selected
	}
	if msg.Button == tea.MouseWheelLeft || msg.Button == tea.MouseWheelRight {
		return false
	}
	if w.expanded {
		w.scrollExpanded(3 * delta)
	} else {
		w.scroll(3 * delta)
	}
	return true
}

func (m *imagePicker) galleryModal() bool {
	return m.focus == "path" || m.folder.busy == "submit" || m.folder.busy == "select"
}

func (p *imagePickerInline) studioClick(msg tea.MouseClickMsg) bool {
	w := p.workspace
	g, wide := imageStudioLayout(w.width, w.height)
	if !wide || w.expanded || p.model.galleryModal() || msg.Button != tea.MouseLeft || msg.Mod != 0 ||
		!w.originKnown || len(w.images) == 0 || len(w.galleryStarts) == 0 {
		return false
	}
	x, y := msg.X-g.previewX, msg.Y-w.origin
	if x < 0 || x >= g.previewWidth || y < w.bodyStart || y > w.bodyEnd {
		return false
	}
	if y == w.bodyEnd {
		openX := g.previewWidth - len(studioOpenLabel)
		switch {
		case x >= openX:
			p.openSelectedImage()
		case x >= openX-len(studioActionGap)-len(studioExpandLabel) && x < openX-len(studioActionGap):
			p.expandImages()
		default:
			return false
		}
		return true
	}
	at := w.offset + y - w.bodyStart
	start := w.galleryStart(w.selected)
	image := &w.images[w.selected]
	if at == start {
		labelWidth := g.previewWidth
		if len(w.images) > 1 {
			labelWidth -= studioPagerWidth
			if x >= labelWidth {
				delta := -1
				if x >= labelWidth+studioPagerWidth/2 {
					delta = 1
				}
				before := w.selected
				w.selectImage(delta)
				if before == w.selected {
					return false
				}
				p.model.focus = "images"
				return true
			}
			labelWidth-- // Match the gap before the first arrow.
		}
		if x < ansi.StringWidth(image.caption(p.model, labelWidth)) {
			p.model.focus = "images"
			return true
		}
	}
	if at > start && at <= start+image.rowCount() && x < image.columns && image.columns <= g.previewWidth {
		if image.nativeRows > 0 {
			if _, placed := w.nativePainted[y]; !placed {
				return false // This row still shows its preparation notice.
			}
		}
		p.expandImages()
		return true
	}
	return false
}

func (p *imagePickerInline) openSelectedImage() {
	w := p.workspace
	if !w.opening && len(w.images) > 0 && p.model.note != imagePickerRequestFailureNote {
		p.model.setNote("", imageSecondary)
	}
	p.openSavedImage()
}
