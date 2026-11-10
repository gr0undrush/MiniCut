package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// libItem is one entry of the media library. Click = preview in the media
// monitor, double-click = append to the timeline, drag = drop on the timeline.
type libItem struct {
	widget.BaseWidget
	app   *App
	m     *Media
	kind  byte // 'v' or 'a' (the folder it lives in)
	bg    *canvas.Rectangle
	last  fyne.Position
	moved bool
}

func newLibItem(a *App, m *Media, kind byte) *libItem {
	it := &libItem{app: a, m: m, kind: kind, bg: canvas.NewRectangle(color.Transparent)}
	it.ExtendBaseWidget(it)
	return it
}

func (it *libItem) CreateRenderer() fyne.WidgetRenderer {
	icon := theme.FileVideoIcon()
	if it.kind == 'a' {
		icon = theme.FileAudioIcon()
	}
	lbl := widget.NewLabel(fmt.Sprintf("%s    %s", it.m.Name, fmtShort(it.m.Duration, false)))
	lbl.Truncation = fyne.TextTruncateEllipsis
	return widget.NewSimpleRenderer(container.NewStack(it.bg,
		container.NewBorder(nil, nil, widget.NewIcon(icon), nil, lbl)))
}

func (it *libItem) setSelected(on bool) {
	if on {
		it.bg.FillColor = withAlpha(colAccent, 90)
	} else {
		it.bg.FillColor = color.Transparent
	}
	it.bg.Refresh()
}

func (it *libItem) Tapped(*fyne.PointEvent) { it.app.selectLib(it.m) }

func (it *libItem) DoubleTapped(*fyne.PointEvent) {
	it.app.selectLib(it.m)
	it.app.appendMedia(it.m, it.kind)
}

func (it *libItem) Dragged(e *fyne.DragEvent) {
	if !it.moved {
		it.app.selectLib(it.m)
	}
	it.moved = true
	it.last = e.AbsolutePosition
	it.app.tl.libDrag(e.AbsolutePosition, it.m)
}

func (it *libItem) DragEnd() {
	if it.moved {
		it.app.tl.libDrop(it.last, it.m)
	}
	it.moved = false
}

// ---- library panel ---------------------------------------------------------------

func (a *App) buildLibrary() fyne.CanvasObject {
	a.libVBox = container.NewVBox()
	a.libABox = container.NewVBox()
	vScroll := container.NewVScroll(a.libVBox)
	vScroll.SetMinSize(fyne.NewSize(380, 150))
	aScroll := container.NewVScroll(a.libABox)
	aScroll.SetMinSize(fyne.NewSize(380, 150))

	vPanel := container.NewBorder(container.NewHBox(
		widget.NewButton("Import video…", func() { a.importMedia('v') }),
		widget.NewButton("Remove", func() { a.removeLib('v') })), nil, nil, nil, vScroll)
	aPanel := container.NewBorder(container.NewHBox(
		widget.NewButton("Import audio…", func() { a.importMedia('a') }),
		widget.NewButton("Remove", func() { a.removeLib('a') })), nil, nil, nil, aScroll)
	a.libTabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Video", theme.FolderIcon(), vPanel),
		container.NewTabItemWithIcon("Audio", theme.FolderIcon(), aPanel))
	a.refreshLibrary()

	hint := widget.NewLabel("Drag onto the timeline · double-click appends · drop files here")
	hint.Truncation = fyne.TextTruncateEllipsis
	return container.NewBorder(nil, hint, nil, nil, a.libTabs)
}

func (a *App) refreshLibrary() {
	a.libItems = nil
	fill := func(box *fyne.Container, list []*Media, kind byte) {
		box.Objects = nil
		for _, m := range list {
			it := newLibItem(a, m, kind)
			it.setSelected(m == a.libSel)
			a.libItems = append(a.libItems, it)
			box.Add(it)
		}
		box.Refresh()
	}
	fill(a.libVBox, a.libV, 'v')
	fill(a.libABox, a.libA, 'a')
}

// selectLib highlights an item and shows it in the media monitor.
func (a *App) selectLib(m *Media) {
	a.libSel = m
	for _, it := range a.libItems {
		it.setSelected(it.m == m)
	}
	a.showMedia(m)
}

func (a *App) removeLib(kind byte) {
	list := &a.libV
	if kind == 'a' {
		list = &a.libA
	}
	for i, m := range *list {
		if m == a.libSel {
			*list = append((*list)[:i], (*list)[i+1:]...)
			a.libSel = nil
			a.refreshLibrary()
			return
		}
	}
}


// importMedia asks for a file and puts it into the Video ('v') or Audio ('a') folder.
func (a *App) importMedia(kind byte) {
	a.pickOpenMulti(func(paths []string) {
		for _, p := range paths {
			m, err := probeMedia(p)
			if err != nil {
				dialog.ShowError(err, a.win)
				continue
			}
			a.addMedia(m, kind)
		}
	})
}

// importAuto files a dropped file by what it contains: video files go to the
// Video folder, audio-only files to the Audio folder.
func (a *App) importAuto(path string) {
	m, err := probeMedia(path)
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	kind := byte('v')
	if m.Video == nil {
		kind = 'a'
	}
	a.addMedia(m, kind)
}

func (a *App) addMedia(m *Media, kind byte) {
	if m.Duration <= 0 {
		a.showErr("No playable media found in this file.")
		return
	}
	if kind == 'v' && m.Video == nil {
		a.showErr("This file has no video. Import audio-only files into the Audio folder.")
		return
	}
	if kind == 'a' && !m.Audio {
		a.showErr("This file has no audio stream.")
		return
	}
	list := &a.libV
	tab := 0
	if kind == 'a' {
		list, tab = &a.libA, 1
	}
	for _, x := range *list {
		if x.Path == m.Path { // already in the folder
			a.libTabs.SelectIndex(tab)
			a.selectLib(x)
			return
		}
	}
	*list = append(*list, m)
	a.libTabs.SelectIndex(tab)
	a.refreshLibrary()
	a.selectLib(m)
}

// appendMedia (double-click) puts the whole item at the end of its lane.
func (a *App) appendMedia(m *Media, kind byte) {
	if kind == 'v' {
		a.insertMedia(m, 'v', len(a.vclips), 0)
	} else {
		a.insertMedia(m, 'a', 0, a.audioEnd())
	}
}
