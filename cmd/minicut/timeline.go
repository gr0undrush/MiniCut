package main

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

const (
	tlRuler  = 20
	tlLane   = 44
	tlGap    = 6
	tlPad    = 6
	tlHeight = tlRuler + 2*tlLane + tlGap + 6
	tlEdge   = 7 // pixels at each end of a clip that grab the trim handle

	minPPS = 0.002 // smallest zoom: pixels per second (3 hours fit easily)
)

var (
	colBG        = color.NRGBA{0x14, 0x16, 0x1a, 0xff}
	colPanel     = color.NRGBA{0x1c, 0x1f, 0x25, 0xff}
	colField     = color.NRGBA{0x25, 0x29, 0x30, 0xff}
	colMuted     = color.NRGBA{0x7d, 0x85, 0x90, 0xff}
	colAccent    = color.NRGBA{0x4f, 0x8c, 0xff, 0xff}
	colBorder    = color.NRGBA{0x2e, 0x33, 0x3b, 0xff}
	colVideo     = color.NRGBA{0x35, 0x60, 0x9f, 0xff}
	colVideoEdge = color.NRGBA{0x5d, 0x8f, 0xe0, 0xff}
	colVideoDim  = color.NRGBA{0x26, 0x34, 0x4b, 0xff}
	colAudio     = color.NRGBA{0x2f, 0x7d, 0x5b, 0xff}
	colAudioEdge = color.NRGBA{0x4c, 0xb0, 0x85, 0xff}
	colAudioDim  = color.NRGBA{0x25, 0x39, 0x2f, 0xff}
	colText      = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	colTextSoft  = color.NRGBA{0xcf, 0xd6, 0xe4, 0xff}
	colPlayhead  = color.NRGBA{0xff, 0x5a, 0x4f, 0xff}
	colHandle    = color.NRGBA{0xff, 0xff, 0xff, 0x70}
)

var tickSteps = []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600}

// what a mouse drag on the timeline is doing
const (
	dragMove = iota
	dragTrimL
	dragTrimR
	dragSeek
)

type tlHit struct {
	x0, x1, y0, y1 float32
	lane           byte // 'v' or 'a'
	i              int
}

type tlDrag struct {
	mode  int
	lane  byte
	i     int
	dx    float32
	moved bool
	off   float64 // audio lane, moving: candidate offset
	orig  Clip    // the clip as it was when the mouse went down
}

// tlGhost is the translucent preview shown while a library item is dragged
// over the timeline.
type tlGhost struct {
	lane byte
	x, w float32
	idx  int     // video lane: insertion index
	off  float64 // audio lane: start position (seconds)
	name string
}

// Timeline is a custom widget: a ruler, a video lane and an audio lane.
// It lives inside a horizontal scroll container.
type Timeline struct {
	widget.BaseWidget
	app    *App
	pps    float64 // pixels per second
	hits   []tlHit
	vg     [][2]float64 // video clip [start,end] on the timeline, in seconds
	drag   *tlDrag
	ghost  *tlGhost
	cursor desktop.Cursor

	// fitMode: the zoom follows the window and the content, so the whole
	// project is always visible without a scroll bar. Zooming by hand ends it;
	// the Fit button turns it back on.
	fitMode bool
}

func NewTimeline(a *App) *Timeline {
	t := &Timeline{app: a, pps: 40, cursor: desktop.DefaultCursor, fitMode: true}
	t.ExtendBaseWidget(t)
	return t
}

func (t *Timeline) CreateRenderer() fyne.WidgetRenderer {
	return &tlRenderer{t: t}
}

// update repaints the timeline and lets the scroll container re-read our width.
func (t *Timeline) update() {
	t.Refresh()
	if t.app.scroll != nil {
		t.app.scroll.Refresh()
	}
}

// span is the length (seconds) the timeline has to show: the content, but at
// least 10 s, plus a margin.
func (t *Timeline) span() float64 { return math.Max(t.app.contentLen(), 10) + 3 }

func (t *Timeline) contentWidth() float32 {
	return float32(t.span()*t.pps + 2*tlPad)
}

// timeAt converts an x position (pixels) to timeline seconds.
func (t *Timeline) timeAt(x float32) float64 { return (float64(x) - tlPad) / t.pps }

func (t *Timeline) hitAt(x, y float32) *tlHit {
	for k := len(t.hits) - 1; k >= 0; k-- {
		h := t.hits[k]
		if x >= h.x0 && x <= h.x1 && y >= h.y0 && y <= h.y1 {
			return &h
		}
	}
	return nil
}

// edgeZone says whether x grabs the left trim handle, the right one, or the
// body of the clip (move). Very narrow clips only offer "move".
func edgeZone(h *tlHit, x float32) int {
	if h.x1-h.x0 < 24 {
		return dragMove
	}
	if x-h.x0 <= tlEdge {
		return dragTrimL
	}
	if h.x1-x <= tlEdge {
		return dragTrimR
	}
	return dragMove
}

// ---- snapping -------------------------------------------------------------------

// snapPoint pulls a time (seconds) to a nearby edit point: 0, the joins of the
// video lane and the edges of the other audio clips (skip = audio clip to ignore).
func (t *Timeline) snapPoint(skip int, p float64) (float64, bool) {
	a := t.app
	cands := []float64{0}
	for _, g := range t.vg {
		cands = append(cands, g[0], g[1])
	}
	for k, o := range a.aclips {
		if k != skip {
			cands = append(cands, o.Offset, o.Offset+o.dur())
		}
	}
	thr := 8 / t.pps
	best, found := p, false
	for _, cd := range cands {
		if d := math.Abs(cd - p); d < thr && (!found || d < math.Abs(best-p)) {
			best, found = cd, true
		}
	}
	return best, found
}

// snapOffset snaps an audio clip that is being moved: its start or its end sticks.
func (t *Timeline) snapOffset(j int, start float64) float64 {
	dur := t.app.aclips[j].dur()
	s, sOK := t.snapPoint(j, start)
	e, eOK := t.snapPoint(j, start+dur)
	e -= dur
	switch {
	case sOK && (!eOK || math.Abs(s-start) <= math.Abs(e-start)):
		return math.Max(0, s)
	case eOK:
		return math.Max(0, e)
	}
	return math.Max(0, start)
}

// ---- trimming -------------------------------------------------------------------

// applyTrim changes the in or out point of the dragged clip. dt is the mouse
// movement in seconds since the button went down. The video lane ripples (the
// clips behind move along); an audio clip keeps its position on the timeline.
func (t *Timeline) applyTrim(d *tlDrag, dt float64) {
	a := t.app
	arr := a.vclips
	if d.lane == 'a' {
		arr = a.aclips
	}
	if d.i >= len(arr) {
		return
	}
	c := d.orig
	switch d.mode {
	case dragTrimL:
		lo := 0.0
		if d.lane == 'a' {
			lo = math.Max(lo, c.Start-c.Offset) // offset must stay >= 0
		}
		ns := math.Max(lo, math.Min(c.Start+dt, c.End-0.05))
		if d.lane == 'a' {
			if left, ok := t.snapPoint(d.i, c.Offset+(ns-c.Start)); ok {
				ns = math.Max(lo, math.Min(c.Start+(left-c.Offset), c.End-0.05))
			}
			arr[d.i].Offset = c.Offset + (ns - c.Start)
		}
		arr[d.i].Start = ns
	case dragTrimR:
		ne := math.Max(c.Start+0.05, c.End+dt)
		if c.Dur > 0 {
			ne = math.Min(ne, c.Dur)
		}
		if d.lane == 'a' {
			if right, ok := t.snapPoint(d.i, c.Offset+(ne-c.Start)); ok {
				ne = c.Start + (right - c.Offset)
				ne = math.Max(c.Start+0.05, ne)
				if c.Dur > 0 {
					ne = math.Min(ne, c.Dur)
				}
			}
		}
		arr[d.i].End = ne
	}
}

// ---- dropping library items -----------------------------------------------------

// relPos converts a window position to timeline coordinates; ok is false when
// the pointer is not over the visible timeline.
func (t *Timeline) relPos(abs fyne.Position) (fyne.Position, bool) {
	if t.app.scroll == nil {
		return fyne.Position{}, false
	}
	drv := fyne.CurrentApp().Driver()
	tp := drv.AbsolutePositionForObject(t)
	sp := drv.AbsolutePositionForObject(t.app.scroll)
	ss := t.app.scroll.Size()
	if abs.X < sp.X || abs.X > sp.X+ss.Width || abs.Y < sp.Y || abs.Y > sp.Y+ss.Height {
		return fyne.Position{}, false
	}
	return fyne.NewPos(abs.X-tp.X, abs.Y-tp.Y), true
}

// dropTarget works out where a dragged library item would land.
func (t *Timeline) dropTarget(abs fyne.Position, m *Media) *tlGhost {
	rel, ok := t.relPos(abs)
	if !ok {
		return nil
	}
	tm := math.Max(0, t.timeAt(rel.X))
	video := rel.Y < float32(tlRuler+tlLane+tlGap/2)
	if rel.Y < tlRuler { // on the ruler: pick the lane that suits the media
		video = m.Video != nil
	}
	w := float32(m.Duration * t.pps)
	if video {
		if m.Video == nil {
			return nil // audio-only files cannot go on the video lane
		}
		idx := 0
		for _, g := range t.vg {
			if (g[0]+g[1])/2 < tm {
				idx++
			}
		}
		bt := 0.0
		if idx < len(t.vg) {
			bt = t.vg[idx][0]
		} else if n := len(t.vg); n > 0 {
			bt = t.vg[n-1][1]
		}
		return &tlGhost{lane: 'v', x: float32(tlPad + bt*t.pps), w: w, idx: idx, name: m.Name}
	}
	if !m.Audio {
		return nil
	}
	off := tm
	if s, ok := t.snapPoint(-1, tm); ok {
		off = s
	}
	return &tlGhost{lane: 'a', x: float32(tlPad + off*t.pps), w: w, off: off, name: m.Name}
}

// libDrag is called while a library item is dragged: it shows the ghost.
func (t *Timeline) libDrag(abs fyne.Position, m *Media) {
	t.ghost = t.dropTarget(abs, m)
	t.Refresh()
}

// libDrop is called when the item is released: it puts the clip on the timeline.
func (t *Timeline) libDrop(abs fyne.Position, m *Media) {
	g := t.dropTarget(abs, m)
	t.ghost = nil
	if g == nil {
		t.Refresh()
		return
	}
	t.app.insertMedia(m, g.lane, g.idx, g.off)
}

// ---- mouse / keyboard -----------------------------------------------------------

func (t *Timeline) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
	a := t.app
	x, y := e.Position.X, e.Position.Y
	if y < tlRuler { // ruler: move the playhead
		t.drag = &tlDrag{mode: dragSeek}
		a.seekProgram(t.timeAt(x))
		return
	}
	h := t.hitAt(x, y)
	if h == nil { // empty lane space: move the playhead there
		a.sel = nil
		t.drag = &tlDrag{mode: dragSeek}
		a.seekProgram(t.timeAt(x))
	} else {
		a.sel = &selection{lane: h.lane, i: h.i}
		d := &tlDrag{mode: edgeZone(h, x), lane: h.lane, i: h.i}
		if c, ok := a.clipAt(h.lane, h.i); ok {
			d.orig, d.off = c, c.Offset
		}
		t.drag = d
	}
	a.selChanged()
	t.Refresh()
}

func (t *Timeline) MouseUp(*desktop.MouseEvent) {
	if t.drag != nil && !t.drag.moved && t.drag.mode != dragSeek {
		t.drag = nil
	}
}

func (t *Timeline) MouseIn(*desktop.MouseEvent) {}

func (t *Timeline) MouseOut() { t.cursor = desktop.DefaultCursor }

func (t *Timeline) MouseMoved(e *desktop.MouseEvent) {
	if t.drag != nil {
		return
	}
	t.cursor = desktop.DefaultCursor
	if h := t.hitAt(e.Position.X, e.Position.Y); h != nil && edgeZone(h, e.Position.X) != dragMove {
		t.cursor = desktop.HResizeCursor
	}
}

func (t *Timeline) Cursor() desktop.Cursor { return t.cursor }

func (t *Timeline) Tapped(*fyne.PointEvent) {}

func (t *Timeline) DoubleTapped(e *fyne.PointEvent) {
	if h := t.hitAt(e.Position.X, e.Position.Y); h != nil {
		t.app.editClip(h.lane, h.i)
	}
}

func (t *Timeline) Dragged(e *fyne.DragEvent) {
	d := t.drag
	if d == nil {
		return
	}
	a := t.app
	if d.mode == dragSeek {
		a.seekProgram(t.timeAt(e.Position.X))
		return
	}
	d.dx += e.Dragged.DX
	if d.dx > 3 || d.dx < -3 {
		d.moved = true
	}
	if !d.moved {
		return
	}
	dt := float64(d.dx) / t.pps
	switch d.mode {
	case dragTrimL, dragTrimR:
		t.applyTrim(d, dt)
	default:
		if d.lane == 'a' && d.i < len(a.aclips) {
			d.off = t.snapOffset(d.i, d.orig.Offset+dt)
		}
	}
	t.Refresh()
}

func (t *Timeline) DragEnd() {
	d := t.drag
	t.drag = nil
	a := t.app
	if d == nil || d.mode == dragSeek || !d.moved {
		t.Refresh()
		return
	}
	switch d.mode {
	case dragTrimL, dragTrimR:
		// the clip was changed live while dragging
	default:
		if d.lane == 'v' && d.i < len(a.vclips) && d.i < len(t.vg) {
			g := t.vg[d.i]
			centre := (g[0]+g[1])/2 + float64(d.dx)/t.pps
			idx := 0
			for j, x := range t.vg {
				if j != d.i && (x[0]+x[1])/2 < centre {
					idx++
				}
			}
			clip := a.vclips[d.i]
			a.vclips = append(a.vclips[:d.i], a.vclips[d.i+1:]...)
			a.vclips = append(a.vclips[:idx], append([]Clip{clip}, a.vclips[idx:]...)...)
			a.sel = &selection{lane: 'v', i: idx}
		} else if d.lane == 'a' && d.i < len(a.aclips) {
			a.aclips[d.i].Offset = d.off
		}
	}
	a.changed(false)
}

func (t *Timeline) FocusGained()   {}
func (t *Timeline) FocusLost()     {}
func (t *Timeline) TypedRune(rune) {}

func (t *Timeline) TypedKey(e *fyne.KeyEvent) {
	a := t.app
	switch e.Name {
	case fyne.KeyDelete, fyne.KeyBackspace:
		a.removeSel()
	case fyne.KeyS:
		a.splitAtPlayhead()
	case fyne.KeyLeft:
		a.seekProgram(a.tpos - 1)
	case fyne.KeyRight:
		a.seekProgram(a.tpos + 1)
	case fyne.KeyHome:
		a.seekProgram(0)
	case fyne.KeyEnd:
		a.seekProgram(a.contentLen())
	}
}

// ---- rendering ------------------------------------------------------------------

type tlRenderer struct {
	t    *Timeline
	objs []fyne.CanvasObject
}

func (r *tlRenderer) Layout(fyne.Size) { r.rebuild() }

func (r *tlRenderer) MinSize() fyne.Size {
	if r.t.fitMode { // never wider than the window, so no scroll bar
		return fyne.NewSize(200, tlHeight)
	}
	return fyne.NewSize(r.t.contentWidth(), tlHeight)
}

func (r *tlRenderer) Refresh() {
	r.rebuild()
	canvas.Refresh(r.t)
}

func (r *tlRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *tlRenderer) Destroy()                     {}

func tlRect(x, y, w, h float32, c color.Color) *canvas.Rectangle {
	rc := canvas.NewRectangle(c)
	rc.Move(fyne.NewPos(x, y))
	rc.Resize(fyne.NewSize(w, h))
	return rc
}

func tlText(s string, x, y, size float32, c color.Color, bold bool) *canvas.Text {
	tx := canvas.NewText(s, c)
	tx.TextSize = size
	tx.TextStyle = fyne.TextStyle{Bold: bold}
	tx.Move(fyne.NewPos(x, y))
	tx.Resize(tx.MinSize())
	return tx
}

func fitText(s string, size float32, style fyne.TextStyle, maxW float32) string {
	if maxW < 12 {
		return ""
	}
	if fyne.MeasureText(s, size, style).Width <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if cand := string(rs) + "…"; fyne.MeasureText(cand, size, style).Width <= maxW {
			return cand
		}
	}
	return ""
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func fmtShort(t float64, frac bool) string {
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", int(t)/3600, int(t)%3600/60, int(t)%60)
	}
	m := int(t) / 60
	s := t - float64(m*60)
	if frac {
		return fmt.Sprintf("%d:%04.1f", m, s)
	}
	return fmt.Sprintf("%d:%02d", m, int(s))
}

func (r *tlRenderer) rebuild() {
	t := r.t
	a := t.app
	if t.fitMode {
		if vw := t.Size().Width; vw > 0 {
			t.pps = math.Max(minPPS, (float64(vw)-2*tlPad-2)/t.span())
		}
	}
	W := t.Size().Width
	if mw := t.contentWidth(); W < mw {
		W = mw
	}
	pps := t.pps
	var objs []fyne.CanvasObject
	t.hits = t.hits[:0]

	// geometry
	t.vg = t.vg[:0]
	tt := 0.0
	for _, c := range a.vclips {
		d := c.dur()
		t.vg = append(t.vg, [2]float64{tt, tt + d})
		tt += d
	}
	dr := t.drag // only a clip being moved needs special drawing
	if dr != nil && (!dr.moved || dr.mode != dragMove) {
		dr = nil
	}
	ag := make([][2]float64, len(a.aclips))
	for j, c := range a.aclips {
		off := c.Offset
		if dr != nil && dr.lane == 'a' && dr.i == j {
			off = dr.off
		}
		ag[j] = [2]float64{off, off + c.dur()}
	}
	T := math.Max(tt, 10)
	for _, g := range ag {
		if g[1] > T {
			T = g[1]
		}
	}

	// lane backgrounds + names
	vy := float32(tlRuler)
	ay := float32(tlRuler + tlLane + tlGap)
	objs = append(objs,
		tlRect(0, vy, W, tlLane, colPanel), tlRect(0, ay, W, tlLane, colPanel),
		tlText("VIDEO", 8, vy+4, 10, colMuted, true), tlText("AUDIO", 8, ay+4, 10, colMuted, true))

	// ruler
	step := tickSteps[len(tickSteps)-1]
	for _, s := range tickSteps {
		if s*pps >= 70 {
			step = s
			break
		}
	}
	for k := 0; float64(k)*step <= T+3; k++ {
		x := float32(tlPad + float64(k)*step*pps)
		objs = append(objs,
			tlRect(x, tlRuler-7, 1, 7, colMuted),
			tlText(fmtShort(float64(k)*step, step < 1), x+3, 2, 10, colMuted, false))
	}
	objs = append(objs, tlRect(0, tlRuler, W, 1, colBorder))

	// clips
	clip := func(lane byte, i int, x0, x1, y0 float32, c Clip, ghost, hollow bool) {
		muted := (lane == 'v' && a.muteV.Checked) || (lane == 'a' && a.muteA.Checked)
		fill, edge := colVideo, colVideoEdge
		if lane == 'v' && muted {
			fill = colVideoDim
		}
		if lane == 'a' {
			fill, edge = colAudio, colAudioEdge
			if muted {
				fill = colAudioDim
			}
		}
		selected := a.sel != nil && a.sel.lane == lane && a.sel.i == i
		top := y0 + 3
		h := float32(tlLane - 6)
		rc := canvas.NewRectangle(fill)
		rc.Move(fyne.NewPos(x0, top))
		rc.Resize(fyne.NewSize(x1-x0, h))
		rc.StrokeColor = edge
		rc.StrokeWidth = 1
		if hollow {
			rc.FillColor = color.Transparent
			objs = append(objs, rc)
			return
		}
		if ghost {
			rc.FillColor = withAlpha(fill, 150)
		}
		if selected {
			rc.StrokeColor = color.White
			rc.StrokeWidth = 2
		}
		objs = append(objs, rc)
		if x1-x0 > 30 {
			name := fitText(filepath.Base(c.Path), 12, fyne.TextStyle{Bold: true}, x1-x0-22)
			if name != "" {
				objs = append(objs, tlText(name, x0+10, y0+5, 12, colText, true))
				objs = append(objs, tlText(fmt.Sprintf("%.2f s", c.dur()), x0+10, y0+22, 11, colTextSoft, false))
			}
		}
		if !ghost {
			if x1-x0 >= 24 { // trim handles
				objs = append(objs, tlRect(x0+1, top+4, 3, h-8, colHandle), tlRect(x1-4, top+4, 3, h-8, colHandle))
			}
			t.hits = append(t.hits, tlHit{x0, x1, y0, y0 + tlLane, lane, i})
		}
	}

	for i, c := range a.vclips {
		x0 := float32(tlPad + t.vg[i][0]*pps)
		x1 := float32(tlPad + t.vg[i][1]*pps)
		clip('v', i, x0, x1, vy, c, false, dr != nil && dr.lane == 'v' && dr.i == i)
	}
	if dr != nil && dr.lane == 'v' && dr.i < len(a.vclips) {
		i := dr.i
		x0 := float32(tlPad+t.vg[i][0]*pps) + dr.dx
		x1 := float32(tlPad+t.vg[i][1]*pps) + dr.dx
		clip('v', i, x0, x1, vy, a.vclips[i], true, false)
	}
	for j, c := range a.aclips {
		clip('a', j, float32(tlPad+ag[j][0]*pps), float32(tlPad+ag[j][1]*pps), ay, c, false, false)
	}

	// library item being dragged over the timeline
	if g := t.ghost; g != nil {
		y0, col := vy, colVideo
		if g.lane == 'a' {
			y0, col = ay, colAudio
		}
		gr := tlRect(g.x, y0+3, g.w, tlLane-6, withAlpha(col, 150))
		gr.StrokeColor = color.White
		gr.StrokeWidth = 1
		objs = append(objs, gr)
		if name := fitText(g.name, 11, fyne.TextStyle{Bold: true}, g.w-16); name != "" {
			objs = append(objs, tlText(name, g.x+8, y0+14, 11, colText, true))
		}
		if g.lane == 'v' { // insertion marker
			objs = append(objs, tlRect(g.x-1, vy, 3, tlLane, colPlayhead))
		}
	}

	// playhead of the timeline player
	px := float32(tlPad + a.tpos*pps)
	objs = append(objs, tlRect(px-1, 0, 2, tlHeight, colPlayhead), tlRect(px-6, 0, 12, 7, colPlayhead))

	r.objs = objs
}
