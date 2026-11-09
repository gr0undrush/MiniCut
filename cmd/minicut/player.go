package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const pvW, pvH = 960, 540 // preview frame size (letterboxed)

// ---- small helpers ----------------------------------------------------------

// playState tracks one player; generations keep a finished player from
// clobbering the state of a newer one.
type playState struct {
	mu      sync.Mutex
	playing bool
	cancel  context.CancelFunc
	gen     int
}

func (p *playState) is() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

func (p *playState) stop() {
	p.mu.Lock()
	c := p.cancel
	p.mu.Unlock()
	if c != nil {
		c()
	}
}

func (p *playState) begin(c context.CancelFunc) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++
	p.cancel, p.playing = c, true
	return p.gen
}

func (p *playState) end(gen int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != gen {
		return false
	}
	p.playing, p.cancel = false, nil
	return true
}

// newStage builds a black video area with a centred message; the caller
// decides its size (see aspectLayout).
func newStage(msg string) (fyne.CanvasObject, *canvas.Image, *widget.Label) {
	img := canvas.NewImageFromImage(nil)
	img.FillMode = canvas.ImageFillContain
	lbl := widget.NewLabelWithStyle(msg, fyne.TextAlignCenter, fyne.TextStyle{})
	stage := container.NewStack(canvas.NewRectangle(color.Black), img, container.NewCenter(lbl))
	return stage, img, lbl
}

// capFPS turns ffprobe's "30000/1001" into a playback rate between 1 and 30 fps.
func capFPS(s string) float64 {
	f := 25.0
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, e1 := strconv.ParseFloat(num, 64)
		d, e2 := strconv.ParseFloat(den, 64)
		if e1 == nil && e2 == nil && d > 0 {
			f = n / d
		}
	} else if v, err := strconv.ParseFloat(s, 64); err == nil {
		f = v
	}
	if f < 1 {
		f = 25
	}
	if f > 30 {
		f = 30
	}
	return f
}

// pumpFrames runs an ffmpeg command that writes raw RGBA 640x360 frames and
// shows them in img. With pace=true frames are spaced in real time (ffmpeg
// itself is not throttled). It returns the time the first frame appeared.
func pumpFrames(ctx context.Context, args []string, img *canvas.Image, fps float64, pace bool,
	onFrame func(n int)) time.Time {

	var start time.Time
	cmd := newCmd(ctx, ffmpegPath, args...)
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return start
	}
	if err := cmd.Start(); err != nil {
		return start
	}
	size := pvW * pvH * 4
	for n := 0; ctx.Err() == nil; n++ {
		buf := make([]byte, size)
		if _, err := io.ReadFull(stdout, buf); err != nil {
			break // end of media, or playback stopped
		}
		if n == 0 {
			start = time.Now()
		} else if pace {
			due := start.Add(time.Duration(float64(n) / fps * float64(time.Second)))
			if d := time.Until(due); d > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(d):
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		img.Image = &image.RGBA{Pix: buf, Stride: pvW * 4, Rect: image.Rect(0, 0, pvW, pvH)}
		img.Refresh()
		onFrame(n)
	}
	cmd.Wait()
	return start
}

// startAudioPipe runs "ffmpeg ... -f wav -" into a window-less ffplay. The
// returned channel closes when ffplay exits; nil means audio could not start.
func startAudioPipe(ctx context.Context, args []string) chan struct{} {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil
	}
	enc := newCmd(ctx, ffmpegPath, args...)
	hideWindow(enc)
	enc.Stdout = pw
	pl := newCmd(ctx, ffplayPath, "-nodisp", "-autoexit", "-loglevel", "quiet", "-i", "pipe:0")
	hideWindow(pl)
	pl.Stdin = pr
	if err := enc.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil
	}
	if err := pl.Start(); err != nil {
		pr.Close()
		pw.Close()
		enc.Process.Kill()
		enc.Wait()
		return nil
	}
	pr.Close() // the children hold their own copies now
	pw.Close()
	done := make(chan struct{})
	go func() {
		pl.Wait()
		close(done)
	}()
	go enc.Wait()
	return done
}

// ---- source player ----------------------------------------------------------

func (a *App) isPlaying() bool { return a.srcPlay.is() }
func (a *App) stopPlay()       { a.srcPlay.stop() }

// setPos moves the source slider and time label without triggering a preview fetch.
func (a *App) setPos(t float64, src *Media) {
	a.silent.Store(true)
	a.slider.SetValue(t)
	a.silent.Store(false)
	a.timeLbl.SetText(fmtT(t) + " / " + fmtT(src.Duration))
}

// play starts / stops in-app playback of the source file from the slider
// position. Moving the slider, loading another file or pressing Stop ends it.
func (a *App) play() {
	if a.srcPlay.is() {
		a.srcPlay.stop()
		return
	}
	if a.src == nil {
		return
	}
	a.pgmPlay.stop()
	src := a.src
	t0 := a.slider.Value
	if t0 >= src.Duration-0.1 {
		t0 = 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	gen := a.srcPlay.begin(cancel)
	a.playBtn.SetText("Stop  ■")

	go func() {
		a.runPlayback(ctx, src, t0)
		cancel() // also stops the audio process
		if a.srcPlay.end(gen) {
			a.playBtn.SetText("Play  ▶")
		}
	}()
}

func (a *App) runPlayback(ctx context.Context, src *Media, t0 float64) {
	var audio *exec.Cmd
	if src.Audio && ffplayPath != "" {
		audio = newCmd(ctx, ffplayPath, "-nodisp", "-autoexit", "-loglevel", "quiet",
			"-ss", ff3(t0), src.Path)
		hideWindow(audio)
		if err := audio.Start(); err != nil {
			audio = nil
		}
	}
	audioDone := make(chan struct{})
	if audio != nil {
		go func() {
			audio.Wait()
			close(audioDone)
		}()
	}

	if src.Video == nil { // audio file: just move the slider while the sound plays
		if audio == nil {
			return
		}
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-audioDone:
				return
			case <-tick.C:
				a.setPos(math.Min(t0+time.Since(start).Seconds(), src.Duration), src)
			}
		}
	}

	if ffmpegPath == "" {
		return
	}
	fps := capFPS(src.Video.FPS)
	vf := fmt.Sprintf("fps=%.3f,scale=%d:%d:force_original_aspect_ratio=decrease,"+
		"pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black", fps, pvW, pvH, pvW, pvH)
	args := []string{"-v", "error", "-re", "-ss", ff3(t0), "-i", src.Path,
		"-an", "-vf", vf, "-pix_fmt", "rgba", "-f", "rawvideo", "-"}
	pumpFrames(ctx, args, a.preview, fps, false, func(n int) {
		a.previewMsg.SetText("")
		a.setPos(math.Min(t0+float64(n)/fps, src.Duration), src)
	})
}

// ---- timeline player (the "program" monitor) ----------------------------------

// previewSettings returns light, fast settings for previewing the timeline:
// 640x360, at most 30 fps, 8 bit. Mute flags come from the timeline checkboxes.
func (a *App) previewSettings(audioOnly bool) Settings {
	s := a.settings()
	s.AudioOnly = audioOnly
	s.VCodec, s.HW, s.RC, s.Rate, s.TwoPass = "libx264", "", "crf", 23, false
	s.Preset, s.Tune, s.Bits, s.GOP, s.Extra = "medium", "none", 8, 0, ""
	s.Res = fmt.Sprintf("%dx%d", pvW, pvH)
	fps := 25.0
	if len(a.vclips) > 0 && a.vclips[0].Video != nil {
		fps = capFPS(a.vclips[0].Video.FPS)
	}
	s.FPS = fmt.Sprintf("%.3f", fps)
	s.ACodec, s.ABitrate = "aac", 128
	return s
}

// setTPos moves the timeline playhead and the time label.
func (a *App) setTPos(t float64, redraw bool) {
	a.tpos = t
	a.pgmSilent.Store(true)
	a.pgmSlider.SetValue(t)
	a.pgmSilent.Store(false)
	a.pgmPos.SetText(fmtT(t))
	a.pgmTotal.SetText("/ " + fmtT(a.contentLen()))
	if redraw {
		a.tl.Refresh()
	}
}

// seekProgram stops the timeline player, moves the playhead to t and shows
// the composed frame at that time.
func (a *App) seekProgram(t float64) {
	a.pgmPlay.stop()
	t = math.Max(0, math.Min(t, a.contentLen()))
	a.setTPos(t, true)
	clips := append([]Clip(nil), a.vclips...)
	st := a.previewSettings(false)
	a.pgmMu.Lock()
	if a.pgmTimer != nil {
		a.pgmTimer.Stop()
	}
	a.pgmTimer = time.AfterFunc(80*time.Millisecond, func() { a.fetchProgramFrame(t, clips, st) })
	a.pgmMu.Unlock()
}

func (a *App) fetchProgramFrame(t float64, clips []Clip, st Settings) {
	a.pgmMu.Lock()
	if a.pgmCancel != nil {
		a.pgmCancel() // abort the stale request
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.pgmCancel = cancel
	a.pgmSeq++
	seq := a.pgmSeq
	a.pgmMu.Unlock()

	img, err := grabTimelineFrame(ctx, &Project{V: clips, S: st}, t)
	if err != nil {
		return
	}
	a.pgmMu.Lock()
	current := seq == a.pgmSeq
	a.pgmMu.Unlock()
	if !current {
		return
	}
	a.pgmImg.Image = img // nil = nothing at this time (black)
	a.pgmImg.Refresh()
	a.pgmMsg.SetText("")
}

// grabTimelineFrame renders the single video frame shown at timeline time t.
// It returns (nil, nil) when the video lane has nothing at that time.
func grabTimelineFrame(ctx context.Context, base *Project, t float64) (image.Image, error) {
	if ffmpegPath == "" {
		return nil, errors.New("ffmpeg not found")
	}
	sub := base.from(t)
	if len(sub.V) == 0 {
		return nil, nil
	}
	q := &Project{V: sub.V[:1], S: base.S} // only the clip under t is needed
	q.S.MuteV, q.S.AudioOnly = true, false
	g, err := q.graph()
	if err != nil || g.v == "" {
		return nil, errors.New("no frame")
	}
	args := append([]string{"-v", "error"}, g.args...)
	args = append(args, "-filter_complex", g.filter, "-map", g.v, "-frames:v", "1",
		"-f", "image2pipe", "-vcodec", "png", "-")
	cmd := newCmd(ctx, ffmpegPath, args...)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, errors.New("no frame")
	}
	return png.Decode(bytes.NewReader(out))
}

// playTimeline starts / stops the timeline player from the playhead.
func (a *App) playTimeline() {
	if a.pgmPlay.is() {
		a.pgmPlay.stop()
		return
	}
	if len(a.vclips) == 0 && len(a.aclips) == 0 {
		a.showErr("The timeline is empty - add some clips first.")
		return
	}
	stV, stA := a.previewSettings(false), a.previewSettings(true)
	chk := &Project{V: a.vclips, A: a.aclips, S: stV}
	if len(a.vclips) == 0 {
		chk.S = stA
	}
	if err := chk.validate(); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	a.srcPlay.stop()
	total := a.contentLen()
	t0 := a.tpos
	if t0 >= total-0.1 {
		t0 = 0
	}
	base := &Project{V: append([]Clip(nil), a.vclips...), A: append([]Clip(nil), a.aclips...)}
	ctx, cancel := context.WithCancel(context.Background())
	gen := a.pgmPlay.begin(cancel)
	a.pgmBtn.SetText("Stop  ■")

	go func() {
		a.runTimelinePlayback(ctx, base, stV, stA, t0, total)
		cancel()
		if a.pgmPlay.end(gen) {
			a.pgmBtn.SetText("Play timeline  ▶")
		}
	}()
}

func (a *App) runTimelinePlayback(ctx context.Context, base *Project, stV, stA Settings, t0, total float64) {
	sub := base.from(t0)

	// sound: the mixed audio of both lanes, piped into a window-less ffplay
	var audioDone chan struct{}
	if ffmpegPath != "" && ffplayPath != "" {
		aq := &Project{V: sub.V, A: sub.A, S: stA}
		if g, err := aq.graph(); err == nil && g.a != "" {
			args := append([]string{"-v", "error"}, g.args...)
			args = append(args, "-filter_complex", g.filter, "-map", g.a, "-f", "wav", "-")
			audioDone = startAudioPipe(ctx, args)
		}
	}

	// picture: the composed video lane as raw frames, paced in real time
	clock := time.Now()
	if len(sub.V) > 0 && ffmpegPath != "" {
		vq := &Project{V: sub.V, S: stV}
		vq.S.MuteV = true
		if g, err := vq.graph(); err == nil && g.v != "" {
			fps, _ := strconv.ParseFloat(stV.FPS, 64)
			if fps < 1 {
				fps = 25
			}
			args := append([]string{"-v", "error"}, g.args...)
			args = append(args, "-filter_complex", g.filter, "-map", g.v,
				"-an", "-pix_fmt", "rgba", "-f", "rawvideo", "-")
			first := pumpFrames(ctx, args, a.pgmImg, fps, true, func(n int) {
				a.pgmMsg.SetText("")
				a.setTPos(math.Min(t0+float64(n)/fps, total), n%3 == 0)
			})
			if !first.IsZero() {
				clock = first
			}
		}
	}

	// the picture ended: keep the playhead moving while the sound continues
	if audioDone != nil {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-audioDone:
				return
			case <-tick.C:
				a.setTPos(math.Min(t0+time.Since(clock).Seconds(), total), true)
			}
		}
	}
}
