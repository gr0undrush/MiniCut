// MiniCut - minimalistic video/audio cutter and joiner.
//
// A native desktop application (Fyne toolkit, compiled Go - no web view, no
// Electron) that drives ffmpeg for all media work.
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed icon.png
var iconPNG []byte

var (
	mediaExts = []string{".mkv", ".mp4", ".mov", ".avi", ".webm", ".ts", ".m2ts", ".mts", ".m4v", ".flv",
		".wmv", ".3gp", ".mp3", ".m4a", ".aac", ".flac", ".wav", ".ogg", ".opus", ".mka"}
	resList = []string{"Source", "3840x2160", "2560x1440", "1920x1080", "1280x720", "854x480"}
	fpsList = []string{"Source", "24", "25", "30", "50", "60"}
)

// ---- dark theme -------------------------------------------------------------

type darkTheme struct{ fyne.Theme }

func (d darkTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBG
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return colField
	case theme.ColorNamePrimary:
		return colAccent
	}
	return d.Theme.Color(n, theme.VariantDark)
}

// ---- helpers ----------------------------------------------------------------

func fmtT(s float64) string {
	if s < 0 {
		s = 0
	}
	h := int(s) / 3600
	m := int(s) % 3600 / 60
	x := s - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%06.3f", h, m, x)
}

func parseT(txt string) (float64, error) {
	v := 0.0
	for _, p := range strings.Split(strings.ReplaceAll(strings.TrimSpace(txt), ",", "."), ":") {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, err
		}
		v = v*60 + f
	}
	return v, nil
}

func mono(text string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(text)
	e.TextStyle = fyne.TextStyle{Monospace: true}
	return e
}

func uriPath(u fyne.URI) string {
	p := u.Path()
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

type disableable interface {
	Enable()
	Disable()
}

func setEnabled(on bool, ws ...disableable) {
	for _, w := range ws {
		if on {
			w.Enable()
		} else {
			w.Disable()
		}
	}
}

// ---- application ------------------------------------------------------------

type selection struct {
	lane byte // 'v' or 'a'
	i    int
}

type App struct {
	fa  fyne.App
	win fyne.Window

	src    *Media
	vclips []Clip
	aclips []Clip
	sel    *selection

	// media library
	libV, libA       []*Media
	libSel           *Media
	libItems         []*libItem
	libVBox, libABox *fyne.Container
	libTabs          *container.AppTabs

	tl     *Timeline
	scroll *container.Scroll

	srcName    *widget.Label
	preview    *canvas.Image
	previewMsg *widget.Label
	slider     *widget.Slider
	timeLbl    *widget.Label
	tlInfo     *widget.Label

	mode, vcodec, encoder, rc, preset, tune, bits, res, fps, acodec *widget.Select
	rate, gop, abr, extra, outPath, offset                 *widget.Entry
	rateUnit                                               *widget.Label
	twoPass, muteV, muteA                                  *widget.Check
	exportBtn                                              *widget.Button
	bar                                                    *widget.ProgressBar
	status                                                 *widget.Label

	playBtn *widget.Button
	srcPlay playState
	pgmPlay playState
	silent  atomic.Bool // set while source playback moves the slider

	// timeline player ("program" monitor)
	pgmImg  *canvas.Image
	pgmMsg  *widget.Label
	pgmSlider *widget.Slider
	pgmPos    *widget.Entry
	pgmTotal  *widget.Label
	pgmBtn    *widget.Button
	pgmSilent atomic.Bool // set while playback moves the position slider
	tpos      float64     // playhead position (seconds)

	pgmMu     sync.Mutex
	pgmTimer  *time.Timer
	pgmCancel context.CancelFunc
	pgmSeq    int

	savedPath string // last path chosen in the Save dialog (it already asked about overwriting)

	exporting bool
	cancel    context.CancelFunc

	frameMu     sync.Mutex
	frameTimer  *time.Timer
	frameCancel context.CancelFunc
	frameSeq    int
}

func main() {
	loadUIScale() // must happen before the app is created
	ffmpegPath = lookTool("ffmpeg")
	ffprobePath = lookTool("ffprobe")
	ffplayPath = lookTool("ffplay")

	fa := app.NewWithID("local.minicut")
	fa.Settings().SetTheme(&darkTheme{theme.DefaultTheme()})
	icon := fyne.NewStaticResource("icon.png", iconPNG)
	fa.SetIcon(icon)
	w := fa.NewWindow("MiniCut")
	w.SetIcon(icon)
	a := &App{fa: fa, win: w}
	w.SetContent(a.build())
	w.Resize(fyne.NewSize(1200, 760))
	w.SetMainMenu(a.mainMenu())
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		for _, u := range uris {
			a.importAuto(uriPath(u))
		}
	})
	w.Canvas().SetOnTypedKey(a.onKey)
	w.SetOnClosed(func() {
		if a.cancel != nil {
			a.cancel()
		}
	})
	if ffmpegPath == "" || ffprobePath == "" {
		dialog.ShowInformation("ffmpeg not found",
			"MiniCut needs ffmpeg and ffprobe.\nInstall ffmpeg (https://ffmpeg.org) and make sure it is in PATH,\n"+
				"or put ffmpeg and ffprobe next to the MiniCut program, then restart it.", w)
	}
	w.ShowAndRun()
}


// ---- output settings ---------------------------------------------------------

func (a *App) codec() string {
	if strings.HasPrefix(a.vcodec.Selected, "H.265") {
		return "libx265"
	}
	return "libx264"
}

func (a *App) refreshUI() {
	codec := a.codec()
	tunes := tuneOptions[codec]
	cur := a.tune.Selected
	a.tune.SetOptions(tunes)
	if !contains(tunes, cur) {
		cur = "none"
	}
	a.tune.SetSelected(cur)

	rc := a.rc.Selected
	r := a.rate.Text
	if rc == "Bitrate" {
		a.rateUnit.SetText("kbps")
		if r == "20" || r == "24" || r == "" {
			a.rate.SetText("8000")
		}
	} else {
		a.rateUnit.SetText("CRF")
		if r == "8000" || r == "20" || r == "24" || r == "" {
			if codec == "libx264" {
				a.rate.SetText("20")
			} else {
				a.rate.SetText("24")
			}
		}
	}
	video := a.mode.Selected == "Video + Audio"
	gpu := strings.HasPrefix(a.encoder.Selected, "GPU")
	setEnabled(video, a.vcodec, a.encoder, a.rc, a.rate, a.preset, a.bits, a.gop, a.extra, a.res, a.fps)
	setEnabled(video && !gpu, a.tune) // hardware encoders have no x264/x265 tunes
	setEnabled(video && rc == "Bitrate" && !gpu, a.twoPass) // ... and no 2-pass
}

func (a *App) settings() Settings {
	rate, _ := strconv.ParseFloat(strings.TrimSpace(a.rate.Text), 64)
	gop, _ := strconv.Atoi(strings.TrimSpace(a.gop.Text))
	abr, _ := strconv.Atoi(strings.TrimSpace(a.abr.Text))
	bits, _ := strconv.Atoi(a.bits.Selected)
	rc := "bitrate"
	if a.rc.Selected == "CRF" {
		rc = "crf"
	}
	ac := "aac"
	switch a.acodec.Selected {
	case "Opus":
		ac = "libopus"
	case "FLAC":
		ac = "flac"
	}
	hw := ""
	for _, e := range hwEncoders {
		if a.encoder.Selected == "GPU: "+e.label {
			hw = e.id
		}
	}
	return Settings{
		AudioOnly: a.mode.Selected == "Audio only",
		VCodec:    a.codec(), HW: hw, RC: rc, Rate: rate, TwoPass: a.twoPass.Checked,
		Preset: a.preset.Selected, Tune: a.tune.Selected, Bits: bits, GOP: gop,
		Extra: a.extra.Text, Res: strings.ToLower(a.res.Selected), FPS: strings.ToLower(a.fps.Selected),
		ACodec: ac, ABitrate: abr, MuteV: a.muteV.Checked, MuteA: a.muteA.Checked,
	}
}

// ---- source / preview --------------------------------------------------------

func (a *App) showErr(msg string) { dialog.ShowError(errors.New(msg), a.win) }

func (a *App) rememberDir(dir string) { a.fa.Preferences().SetString("dir", dir) }

func (a *App) setDialogDir(d *dialog.FileDialog) {
	dir := a.fa.Preferences().String("dir")
	if dir == "" {
		return
	}
	if lister, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
		d.SetLocation(lister)
	}
}




func (a *App) onScrub() {
	if a.src == nil || a.silent.Load() {
		return
	}
	if a.isPlaying() { // the user moved the slider: stop playback
		a.stopPlay()
	}
	a.timeLbl.SetText(fmtT(a.slider.Value) + " / " + fmtT(a.src.Duration))
	t := a.slider.Value
	a.frameMu.Lock()
	if a.frameTimer != nil {
		a.frameTimer.Stop()
	}
	a.frameTimer = time.AfterFunc(100*time.Millisecond, func() { a.fetchFrame(t) })
	a.frameMu.Unlock()
}

func (a *App) fetchFrame(t float64) {
	src := a.src
	if src == nil {
		return
	}
	if src.Video == nil {
		a.preview.Image = nil
		a.preview.Refresh()
		a.previewMsg.SetText("♪  audio only")
		return
	}
	a.frameMu.Lock()
	if a.frameCancel != nil {
		a.frameCancel() // abort the stale request
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.frameCancel = cancel
	a.frameSeq++
	seq := a.frameSeq
	a.frameMu.Unlock()

	img, err := grabFrame(ctx, src.Path, t)
	if err != nil {
		return
	}
	a.frameMu.Lock()
	current := seq == a.frameSeq
	a.frameMu.Unlock()
	if !current {
		return
	}
	a.preview.Image = img
	a.preview.Refresh()
	a.previewMsg.SetText("")
}

func (a *App) seek(d float64) {
	if a.src == nil {
		return
	}
	a.slider.SetValue(math.Min(math.Max(0, a.slider.Value+d), a.src.Duration))
}




// ---- timeline model ----------------------------------------------------------






func (a *App) removeSel() {
	if a.sel == nil {
		return
	}
	i := a.sel.i
	if a.sel.lane == 'v' && i < len(a.vclips) {
		a.vclips = append(a.vclips[:i], a.vclips[i+1:]...)
	} else if a.sel.lane == 'a' && i < len(a.aclips) {
		a.aclips = append(a.aclips[:i], a.aclips[i+1:]...)
	}
	a.sel = nil
	a.changed(false)
}

func (a *App) clearAll() {
	if len(a.vclips) == 0 && len(a.aclips) == 0 {
		return
	}
	dialog.ShowConfirm("Clear timeline", "Remove all clips from the timeline?", func(ok bool) {
		if ok {
			a.vclips, a.aclips, a.sel = nil, nil, nil
			a.changed(false)
		}
	}, a.win)
}

func (a *App) applyOffset() {
	if a.sel == nil || a.sel.lane != 'a' || a.sel.i >= len(a.aclips) {
		return
	}
	t, err := parseT(a.offset.Text)
	if err != nil {
		return
	}
	a.aclips[a.sel.i].Offset = math.Max(0, t)
	a.changed(false)
}

// editClip loads a timeline clip back into the trimmer (double-click).

func (a *App) vTotal() float64 {
	t := 0.0
	for _, c := range a.vclips {
		t += c.dur()
	}
	return t
}

func (a *App) contentLen() float64 {
	m := a.vTotal()
	for _, c := range a.aclips {
		if e := c.Offset + c.dur(); e > m {
			m = e
		}
	}
	return m
}

func (a *App) updateInfo() {
	a.tlInfo.SetText(fmt.Sprintf("video %s ×%d · audio ×%d",
		fmtT(a.vTotal()), len(a.vclips), len(a.aclips)))
}

func (a *App) changed(_ bool) {
	a.tl.update()
	a.selChanged()
	a.updateInfo()
	a.pgmSlider.Max = math.Max(a.contentLen(), 1)
	a.pgmSlider.Refresh()
	a.seekProgram(a.tpos)
}

// jumpCut moves the playhead to the previous (dir<0) or next (dir>0) edit
// point: the start, the joins between video clips, and audio clip edges.
func (a *App) jumpCut(dir int) {
	pts := []float64{0}
	t := 0.0
	for _, c := range a.vclips {
		t += c.dur()
		pts = append(pts, t)
	}
	for _, c := range a.aclips {
		pts = append(pts, c.Offset, c.Offset+c.dur())
	}
	sort.Float64s(pts)
	cur := a.tpos
	target := 0.0
	if dir < 0 {
		for _, p := range pts {
			if p < cur-0.01 {
				target = p
			}
		}
	} else {
		target = a.contentLen()
		for k := len(pts) - 1; k >= 0; k-- {
			if pts[k] > cur+0.01 {
				target = pts[k]
			}
		}
	}
	a.seekProgram(target)
}

func (a *App) selChanged() {
	if a.sel != nil && a.sel.lane == 'a' && a.sel.i < len(a.aclips) {
		a.offset.Enable()
		a.offset.SetText(fmtT(a.aclips[a.sel.i].Offset))
	} else {
		a.offset.SetText("")
		a.offset.Disable()
	}
}





// ---- export ------------------------------------------------------------------

func (a *App) doExport() {
	if a.exporting {
		if a.cancel != nil {
			a.status.SetText("Cancelling…")
			a.cancel()
		}
		return
	}
	out := strings.TrimSpace(a.outPath.Text)
	if out == "" {
		a.pickSave(func(p string) {
			a.outPath.SetText(p)
			a.startExport(p)
		})
		return
	}
	a.startExport(out)
}

func (a *App) startExport(out string) {
	if ffmpegPath == "" {
		a.showErr("ffmpeg not found in PATH.")
		return
	}
	p := &Project{V: a.vclips, A: a.aclips, S: a.settings()}
	if err := p.validate(); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	if !p.S.AudioOnly && p.S.HW != "" {
		if name := hwEncoderName(p.S.HW, p.S.VCodec); !hwOK(name) {
			a.showErr(fmt.Sprintf("%s is not available on this computer (ffmpeg could not start it).\n"+
				"Choose the CPU encoder, another GPU, or the other codec.", name))
			return
		}
	}
	if !filepath.IsAbs(out) {
		a.showErr("Choose an output file (full path).")
		return
	}
	ext := ".mkv"
	if p.S.AudioOnly {
		ext = ".mka"
	}
	out = strings.TrimSuffix(out, filepath.Ext(out)) + ext
	a.outPath.SetText(out)
	if st, err := os.Stat(filepath.Dir(out)); err != nil || !st.IsDir() {
		a.showErr("The output folder does not exist.")
		return
	}
	for _, c := range allClips(p) {
		if sameFile(c.Path, out) {
			a.showErr("The output file would overwrite a source file.")
			return
		}
	}
	// A zero-size file is the empty placeholder the save dialog just created.
	if st, err := os.Stat(out); err == nil && st.Size() > 0 && out != a.savedPath {
		dialog.ShowConfirm("Overwrite file?", filepath.Base(out)+" already exists.\nOverwrite it?",
			func(ok bool) {
				if ok {
					a.runExport(p, out)
				}
			}, a.win)
		return
	}
	a.runExport(p, out)
}

func (a *App) runExport(p *Project, out string) {
	tmp, err := os.MkdirTemp("", "minicut_")
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	cmds, total, err := p.build(out, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		dialog.ShowError(err, a.win)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel, a.exporting = cancel, true
	a.exportBtn.SetText("Cancel")
	a.bar.SetValue(0)
	a.status.SetText("Starting…")

	go func() {
		res := runJob(ctx, cmds, total, out, tmp, func(pct float64, txt string) {
			a.bar.SetValue(pct / 100)
			a.status.SetText(txt)
		})
		cancel()
		a.exporting, a.cancel = false, nil
		a.exportBtn.SetText("Export")
		switch {
		case res.cancelled:
			a.bar.SetValue(0)
			a.status.SetText("Cancelled")
		case res.err != nil:
			a.status.SetText("Failed")
			msg := res.err.Error()
			if len(msg) > 1500 {
				msg = msg[len(msg)-1500:]
			}
			a.showErr(msg)
		default:
			a.bar.SetValue(1)
			a.status.SetText("Done")
			dialog.ShowInformation("Export finished", "Saved:\n"+out, a.win)
		}
	}()
}

// detectGPU runs in the background at start-up and adds the GPU encoders that
// really work on this computer to the Encoder list.
func (a *App) detectGPU() {
	labels := detectHW()
	if len(labels) == 0 {
		return
	}
	a.encoder.SetOptions(append([]string{cpuEncoderLabel}, labels...))
	a.encoder.Refresh()
}

func (a *App) build() fyne.CanvasObject {
	sel := func(opts []string, def string) *widget.Select {
		s := widget.NewSelect(opts, nil)
		s.SetSelected(def)
		return s
	}
	bold := func(s string) *widget.Label {
		return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	timeEntry := func(e *widget.Entry) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(112, 36), e)
	}
	const wide = 16.0 / 9.0

	// ---- media monitor (upper left): the picture grows with the window
	a.srcName = widget.NewLabel("Pick an item in the media library")
	stage, img, msg := newStage("Media monitor")
	a.preview, a.previewMsg = img, msg
	a.slider = widget.NewSlider(0, 1)
	a.slider.Step = 0.001
	a.slider.OnChanged = func(float64) { a.onScrub() }
	a.timeLbl = widget.NewLabel("00:00:00.000 / 00:00:00.000")
	step := func(label string, d float64) *widget.Button {
		return widget.NewButton(label, func() { a.seek(d) })
	}
	a.playBtn = widget.NewButton("Play  ▶", a.play)
	a.playBtn.Importance = widget.HighImportance
	left := container.NewBorder(
		container.NewHBox(bold("Media monitor"), a.srcName),
		container.NewVBox(a.slider, container.NewHBox(a.playBtn, a.timeLbl, layout.NewSpacer(),
			step("−1s", -1), step("−.1", -0.1), step("+.1", 0.1), step("+1s", 1))),
		nil, nil,
		container.New(aspectLayout{wide}, stage))

	// ---- timeline player (upper right)
	pgmStage, pimg, pmsg := newStage("Timeline preview")
	a.pgmImg, a.pgmMsg = pimg, pmsg
	a.pgmSlider = widget.NewSlider(0, 1)
	a.pgmSlider.Step = 0.01
	a.pgmSlider.OnChanged = func(v float64) {
		if !a.pgmSilent.Load() {
			a.seekProgram(v)
		}
	}
	a.pgmPos = mono("00:00:00.000")
	a.pgmPos.OnSubmitted = func(s string) {
		if t, err := parseT(s); err == nil {
			a.seekProgram(t)
		}
	}
	a.pgmTotal = widget.NewLabel("/ 00:00:00.000")
	pstep := func(label string, d float64) *widget.Button {
		return widget.NewButton(label, func() { a.seekProgram(a.tpos + d) })
	}
	a.pgmBtn = widget.NewButton("Play timeline  ▶", a.playTimeline)
	a.pgmBtn.Importance = widget.HighImportance
	a.tlInfo = widget.NewLabel("")
	right := container.NewBorder(
		container.NewHBox(bold("Timeline player"), a.pgmBtn,
			widget.NewButtonWithIcon("Split", theme.ContentCutIcon(), a.splitAtPlayhead),
			layout.NewSpacer(), a.tlInfo),
		container.NewVBox(a.pgmSlider, container.NewHBox(timeEntry(a.pgmPos), a.pgmTotal, layout.NewSpacer(),
			widget.NewButton("|◀", func() { a.jumpCut(-1) }), pstep("−1s", -1), pstep("−.1", -0.1),
			pstep("+.1", 0.1), pstep("+1s", 1), widget.NewButton("▶|", func() { a.jumpCut(1) }))),
		nil, nil,
		container.New(aspectLayout{wide}, pgmStage))

	top := container.NewGridWithColumns(2, container.NewPadded(left), container.NewPadded(right))

	// ---- timeline (middle): all cutting happens here
	a.tl = NewTimeline(a)
	a.scroll = container.NewHScroll(a.tl)
	a.scroll.SetMinSize(fyne.NewSize(400, tlHeight+16))
	a.muteV = widget.NewCheck("Mute video lane", func(bool) { a.tl.update() })
	a.muteA = widget.NewCheck("Mute audio lane", func(bool) { a.tl.update() })
	a.offset = mono("")
	a.offset.Disable()
	a.offset.OnSubmitted = func(string) { a.applyOffset() }
	tools := container.NewHBox(
		bold("Timeline"),
		widget.NewButton("−", func() { a.zoom(1 / 1.5) }),
		widget.NewButton("+", func() { a.zoom(1.5) }),
		widget.NewButton("Fit", a.fit),
		widget.NewButtonWithIcon("Split at playhead", theme.ContentCutIcon(), a.splitAtPlayhead),
		widget.NewButton("Remove", a.removeSel),
		widget.NewButton("Clear all", a.clearAll),
		a.muteV, a.muteA,
		widget.NewLabel("Audio clip starts at"), timeEntry(a.offset))
	tlBlock := container.NewVBox(tools, a.scroll)

	// ---- export settings + progress (lower right)
	a.mode = sel([]string{"Video + Audio", "Audio only"}, "Video + Audio")
	a.vcodec = sel([]string{"H.264 (x264)", "H.265 (x265)"}, "H.264 (x264)")
	a.encoder = sel([]string{cpuEncoderLabel}, cpuEncoderLabel)
	a.rc = sel([]string{"Bitrate", "CRF"}, "Bitrate")
	a.rate = widget.NewEntry()
	a.rate.SetText("8000")
	a.rateUnit = widget.NewLabel("kbps")
	a.twoPass = widget.NewCheck("2-pass", nil)
	a.preset = sel(presetList, "medium")
	a.tune = sel(tuneOptions["libx264"], "none")
	a.bits = sel([]string{"8", "10"}, "8")
	a.gop = widget.NewEntry()
	a.gop.SetPlaceHolder("auto")
	a.res = sel(resList, "Source")
	a.fps = sel(fpsList, "Source")
	a.acodec = sel([]string{"AAC", "Opus", "FLAC"}, "AAC")
	a.abr = widget.NewEntry()
	a.abr.SetText("192")
	a.extra = widget.NewEntry()
	a.extra.SetPlaceHolder("extra ffmpeg args")

	rateRow := container.NewBorder(nil, nil, nil, container.NewHBox(a.rateUnit, a.twoPass), a.rate)
	forms := container.NewGridWithColumns(2,
		widget.NewForm(
			widget.NewFormItem("Mode", a.mode),
			widget.NewFormItem("Video codec", a.vcodec),
			widget.NewFormItem("Encoder", a.encoder),
			widget.NewFormItem("Rate control", a.rc),
			widget.NewFormItem("Rate", rateRow),
			widget.NewFormItem("Preset", a.preset),
			widget.NewFormItem("Tune", a.tune)),
		widget.NewForm(
			widget.NewFormItem("Bit depth", a.bits),
			widget.NewFormItem("GOP", a.gop),
			widget.NewFormItem("Resolution", a.res),
			widget.NewFormItem("Frame rate", a.fps),
			widget.NewFormItem("Audio codec", a.acodec),
			widget.NewFormItem("Audio kbps", a.abr),
			widget.NewFormItem("Extra args", a.extra)))

	a.outPath = widget.NewEntry()
	a.outPath.SetPlaceHolder("choose where to save the .mkv")
	browse := widget.NewButton("…", func() { a.pickSave(func(p string) { a.outPath.SetText(p) }) })
	a.exportBtn = widget.NewButton("Export", a.doExport)
	a.exportBtn.Importance = widget.HighImportance
	a.bar = widget.NewProgressBar()
	a.status = widget.NewLabel("Ready")
	exportPanel := container.NewVBox(
		container.New(capLayout{max: 960}, forms),
		container.NewBorder(nil, nil, widget.NewLabel("Save as"), container.NewHBox(browse, a.exportBtn), a.outPath),
		container.NewBorder(nil, nil, nil, a.status, a.bar))

	// ---- media library (lower left)
	library := a.buildLibrary()
	// library and export are two cards of exactly the same size
	bottom := container.New(splitLayout{left: 0.5},
		container.NewPadded(widget.NewCard("Media library", "", library)),
		container.NewPadded(widget.NewCard("Export", "", exportPanel)))

	// callbacks that depend on every widget existing
	a.mode.OnChanged = func(string) { a.refreshUI() }
	a.vcodec.OnChanged = func(string) { a.refreshUI() }
	a.rc.OnChanged = func(string) { a.refreshUI() }
	a.encoder.OnChanged = func(string) { a.refreshUI() }
	a.refreshUI()
	a.updateInfo()
	go a.detectGPU()

	// pageLayout: the timeline and the bottom row take the height they need,
	// the monitors get all the rest. The scroll is only a safety net.
	page := container.NewVScroll(container.NewPadded(container.New(pageLayout{}, top, tlBlock, bottom)))
	page.SetMinSize(fyne.NewSize(900, 400))
	return page
}

func (a *App) onKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyLeft:
		a.seek(-1)
	case fyne.KeyRight:
		a.seek(1)
	case fyne.KeyS:
		a.splitAtPlayhead()
	case fyne.KeyF11:
		a.win.SetFullScreen(!a.win.FullScreen())
	case fyne.KeyDelete, fyne.KeyBackspace:
		a.removeSel()
	}
}

// showMedia puts a library item (or a clip's source) into the media monitor.
func (a *App) showMedia(m *Media) {
	a.stopPlay()
	a.src = m
	a.srcName.SetText(m.Name)
	a.slider.Max = m.Duration
	a.slider.SetValue(0)
	a.slider.Refresh()
	a.onScrub()
}

// editClip (double-click on a timeline clip) previews its source at the clip's in point.
func (a *App) editClip(lane byte, i int) {
	c, ok := a.clipAt(lane, i)
	if !ok {
		return
	}
	dur := c.Dur
	if dur <= 0 {
		dur = c.End
	}
	a.showMedia(&Media{Path: c.Path, Name: filepath.Base(c.Path), Duration: dur, Video: c.Video, Audio: c.Audio})
	a.slider.SetValue(c.Start)
	a.onScrub()
}

func (a *App) clipAt(lane byte, i int) (Clip, bool) {
	if lane == 'v' && i >= 0 && i < len(a.vclips) {
		return a.vclips[i], true
	}
	if lane == 'a' && i >= 0 && i < len(a.aclips) {
		return a.aclips[i], true
	}
	return Clip{}, false
}

// audioEnd is where the last audio clip ends (0 when the lane is empty).
func (a *App) audioEnd() float64 {
	end := 0.0
	for _, c := range a.aclips {
		if e := c.Offset + c.dur(); e > end {
			end = e
		}
	}
	return end
}

// insertMedia puts a whole media file on the timeline: into the video lane at
// index idx, or onto the audio lane starting at off seconds. It can then be
// trimmed and moved on the timeline.
func (a *App) insertMedia(m *Media, lane byte, idx int, off float64) {
	c := Clip{Path: m.Path, Start: 0, End: m.Duration, Dur: m.Duration, Audio: m.Audio, Video: m.Video}
	if lane == 'v' {
		if m.Video == nil {
			a.showErr("Only files with video can go on the video lane.")
			return
		}
		if idx < 0 || idx > len(a.vclips) {
			idx = len(a.vclips)
		}
		a.vclips = append(a.vclips[:idx], append([]Clip{c}, a.vclips[idx:]...)...)
		a.sel = &selection{lane: 'v', i: idx}
	} else {
		if !m.Audio {
			a.showErr("This file has no audio stream.")
			return
		}
		c.Offset = math.Max(0, off)
		a.aclips = append(a.aclips, c)
		a.sel = &selection{lane: 'a', i: len(a.aclips) - 1}
	}
	a.changed(true)
}

// splitAtPlayhead cuts the clips under the playhead in two (razor).

// ---- UI size ------------------------------------------------------------------
// Fyne scales the interface from the system DPI setting. If that does not suit
// the monitor, View -> UI size stores a factor that is applied at the next start
// (it becomes the FYNE_SCALE environment variable before the app is created).

func uiScalePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "minicut", "uiscale.txt")
}

func loadUIScale() {
	p := uiScalePath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	s := strings.TrimSpace(string(b))
	if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0.5 && f <= 4 {
		os.Setenv("FYNE_SCALE", s)
	}
}

func saveUIScale(val string) error {
	p := uiScalePath()
	if p == "" {
		return errors.New("no config folder available")
	}
	if val == "" { // automatic
		os.Remove(p)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(val), 0o644)
}

func (a *App) mainMenu() *fyne.MainMenu {
	opts := []struct{ label, val string }{
		{"Automatic (system setting)", ""}, {"75 %", "0.75"}, {"100 %", "1"},
		{"125 %", "1.25"}, {"150 %", "1.5"}, {"175 %", "1.75"}, {"200 %", "2"},
	}
	var items []*fyne.MenuItem
	for _, o := range opts {
		val := o.val
		items = append(items, fyne.NewMenuItem(o.label, func() { a.setUIScale(val) }))
	}
	return fyne.NewMainMenu(
		fyne.NewMenu("View", fyne.NewMenuItem("Full screen (F11)", func() { a.win.SetFullScreen(!a.win.FullScreen()) })),
		fyne.NewMenu("UI size", items...))
}

func (a *App) setUIScale(val string) {
	if err := saveUIScale(val); err != nil {
		a.showErr(err.Error())
		return
	}
	dialog.ShowConfirm("UI size", "The new size is used the next time MiniCut starts.\nRestart now?",
		func(ok bool) {
			if ok {
				a.restart()
			}
		}, a.win)
}

func (a *App) restart() {
	exe, err := os.Executable()
	if err != nil {
		a.showErr(err.Error())
		return
	}
	cmd := exec.Command(exe)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		a.showErr(err.Error())
		return
	}
	a.fa.Quit()
}

// fit turns on the timeline's fit mode: the zoom follows the window and the
// content, so the whole project is visible without a scroll bar (even 3 hours).
func (a *App) fit() {
	a.tl.fitMode = true
	a.scroll.Offset = fyne.NewPos(0, 0)
	a.tl.update()
}

// zoom zooms by hand (this ends fit mode).
func (a *App) zoom(f float64) {
	a.tl.fitMode = false
	a.tl.pps = math.Min(400, math.Max(minPPS, a.tl.pps*f))
	a.tl.update()
}

// ---- splitting ----------------------------------------------------------------

// vclipStart is where video-lane clip i starts on the timeline (seconds).
func (a *App) vclipStart(i int) float64 {
	acc := 0.0
	for k := 0; k < i && k < len(a.vclips); k++ {
		acc += a.vclips[k].dur()
	}
	return acc
}

// splitVideo cuts video-lane clip i into two clips at timeline time t.
// The right half becomes clip i+1. It reports false when t is not inside the clip.
func (a *App) splitVideo(i int, t float64) bool {
	if i < 0 || i >= len(a.vclips) {
		return false
	}
	c := a.vclips[i]
	start := a.vclipStart(i)
	if !(t > start+0.05 && t < start+c.dur()-0.05) {
		return false
	}
	cut := c.Start + (t - start)
	left, right := c, c
	left.End, right.Start = cut, cut
	a.vclips = append(a.vclips[:i], append([]Clip{left, right}, a.vclips[i+1:]...)...)
	return true
}

// splitAudio cuts audio-lane clip j into two clips at timeline time t.
func (a *App) splitAudio(j int, t float64) bool {
	if j < 0 || j >= len(a.aclips) {
		return false
	}
	c := a.aclips[j]
	if !(t > c.Offset+0.05 && t < c.Offset+c.dur()-0.05) {
		return false
	}
	cut := c.Start + (t - c.Offset)
	left, right := c, c
	left.End, right.Start, right.Offset = cut, cut, t
	a.aclips = append(a.aclips[:j], append([]Clip{left, right}, a.aclips[j+1:]...)...)
	return true
}

// splitAtPlayhead cuts the clip at the playhead into two clips. If a clip is
// selected and the playhead is inside it, only that clip is cut; otherwise every
// clip under the playhead is cut (video lane and audio lane). The right half
// gets selected.
func (a *App) splitAtPlayhead() {
	t := a.tpos
	var next *selection
	if s := a.sel; s != nil {
		switch {
		case s.lane == 'v' && a.splitVideo(s.i, t):
			next = &selection{lane: 'v', i: s.i + 1}
		case s.lane == 'a' && a.splitAudio(s.i, t):
			next = &selection{lane: 'a', i: s.i + 1}
		}
	}
	if next == nil {
		for i := range a.vclips {
			if a.splitVideo(i, t) {
				next = &selection{lane: 'v', i: i + 1}
				break
			}
		}
		for j := 0; j < len(a.aclips); j++ {
			if a.splitAudio(j, t) {
				if next == nil {
					next = &selection{lane: 'a', i: j + 1}
				}
				j++ // skip the new right half
			}
		}
	}
	if next == nil {
		a.showErr("Move the playhead onto a clip first (click the ruler or the timeline), then split.")
		return
	}
	a.sel = next
	a.changed(false)
}
