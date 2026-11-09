package main

import "fyne.io/fyne/v2"

const pageGap = 8

// aspectLayout keeps its child at a fixed aspect ratio, as large as the space
// allows, centred. It is what makes the video monitors grow with the window.
type aspectLayout struct{ ratio float32 }

func (l aspectLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w, h := size.Width, size.Height
	if h <= 0 || w <= 0 {
		return
	}
	if w/h > l.ratio {
		w = h * l.ratio
	} else {
		h = w / l.ratio
	}
	for _, o := range objs {
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos((size.Width-w)/2, (size.Height-h)/2))
	}
}

func (l aspectLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(160, 90) }

// capLayout gives its child at most `max` width (left aligned) so rows of
// small controls do not stretch across a very wide window.
type capLayout struct{ max float32 }

func (l capLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := size.Width
	if w > l.max {
		w = l.max
	}
	for _, o := range objs {
		o.Resize(fyne.NewSize(w, size.Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l capLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m
}

// splitLayout puts two children side by side; the first gets `left` of the width.
type splitLayout struct{ left float32 }

func (l splitLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) != 2 {
		return
	}
	lw := size.Width * l.left
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(lw, size.Height))
	objs[1].Move(fyne.NewPos(lw, 0))
	objs[1].Resize(fyne.NewSize(size.Width-lw, size.Height))
}

func (l splitLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) != 2 {
		return fyne.Size{}
	}
	a, b := objs[0].MinSize(), objs[1].MinSize()
	w := a.Width / l.left
	if x := b.Width / (1 - l.left); x > w {
		w = x
	}
	h := a.Height
	if b.Height > h {
		h = b.Height
	}
	return fyne.NewSize(w, h)
}

// pageLayout stacks [top, timeline, bottom]. The timeline and the bottom row
// get exactly the height they need; the monitors (top) get everything else.
// That is how the same window fits a 1080p screen and uses a 4K one.
type pageLayout struct{}

func (pageLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) != 3 {
		return
	}
	tlH := objs[1].MinSize().Height
	bH := objs[2].MinSize().Height
	topH := size.Height - tlH - bH - 2*pageGap
	if m := objs[0].MinSize().Height; topH < m {
		topH = m
	}
	y := float32(0)
	for i, h := range []float32{topH, tlH, bH} {
		objs[i].Move(fyne.NewPos(0, y))
		objs[i].Resize(fyne.NewSize(size.Width, h))
		y += h + pageGap
	}
}

func (pageLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) != 3 {
		return fyne.Size{}
	}
	var w, h float32
	for _, o := range objs {
		m := o.MinSize()
		if m.Width > w {
			w = m.Width
		}
		h += m.Height
	}
	return fyne.NewSize(w, h+2*pageGap)
}
