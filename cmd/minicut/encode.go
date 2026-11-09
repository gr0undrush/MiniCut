package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---- project model --------------------------------------------------------

type Clip struct {
	Path   string
	Start  float64
	End    float64
	Offset float64 // audio lane only: position on the timeline
	Dur    float64 // length of the whole source file (limit for trimming)
	Audio  bool
	Video  *VideoInfo
}

func (c Clip) dur() float64 { return c.End - c.Start }

type Settings struct {
	AudioOnly bool
	VCodec    string // libx264 | libx265
	HW        string // "" = CPU, otherwise a hwEncoders id (nvenc, qsv, amf, videotoolbox)
	RC        string // bitrate | crf
	Rate      float64
	TwoPass   bool
	Preset    string
	Tune      string
	Bits      int
	GOP       int
	Extra     string
	Res       string // source | WxH
	FPS       string // source | number
	ACodec    string
	ABitrate  int
	MuteV     bool
	MuteA     bool
}

type Project struct {
	V []Clip
	A []Clip
	S Settings
}

var (
	presetList = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	tuneOptions = map[string][]string{
		"libx264": {"none", "film", "animation", "grain", "stillimage", "fastdecode", "zerolatency"},
		"libx265": {"none", "animation", "grain", "fastdecode", "zerolatency", "psnr", "ssim"},
	}
	acodecs = map[string]bool{"aac": true, "libopus": true, "flac": true}
	fpsRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(/[0-9]+)?$`)
	resRe   = regexp.MustCompile(`^[0-9]{2,5}x[0-9]{2,5}$`)
	progRe  = regexp.MustCompile(`^(frame|fps|stream_\d+_\d+_q|bitrate|total_size|out_time_us|out_time_ms|out_time|dup_frames|drop_frames|speed|progress)=(.*)$`)
)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func allClips(p *Project) []Clip {
	all := make([]Clip, 0, len(p.V)+len(p.A))
	all = append(all, p.V...)
	return append(all, p.A...)
}

// validate checks everything that ends up on the ffmpeg command line.
func (p *Project) validate() error {
	s := &p.S
	if len(p.V) == 0 && len(p.A) == 0 {
		return errors.New("the timeline is empty - add some clips first")
	}
	for _, c := range allClips(p) {
		if !filepath.IsAbs(c.Path) {
			return fmt.Errorf("bad clip path %q", c.Path)
		}
		if c.Start < 0 || c.dur() < 0.05 || c.Offset < 0 {
			return fmt.Errorf("invalid time range in %s", filepath.Base(c.Path))
		}
	}
	for _, c := range p.A {
		if !c.Audio {
			return fmt.Errorf("%s has no audio stream", filepath.Base(c.Path))
		}
	}
	if !s.AudioOnly {
		if len(p.V) == 0 {
			return errors.New("the video lane is empty (or switch Mode to Audio only)")
		}
		for _, c := range p.V {
			if c.Video == nil {
				return fmt.Errorf("%s has no video stream; remove it or switch Mode to Audio only", filepath.Base(c.Path))
			}
		}
		if s.VCodec != "libx264" && s.VCodec != "libx265" {
			return errors.New("unknown video codec")
		}
		if !contains(presetList, s.Preset) {
			return errors.New("unknown preset")
		}
		if s.Tune != "none" && !contains(tuneOptions[s.VCodec], s.Tune) {
			return errors.New("unknown tune for this codec")
		}
		if s.RC != "crf" && s.RC != "bitrate" {
			return errors.New("unknown rate control")
		}
		if !(s.Rate > 0) || s.Rate > 1e6 {
			return errors.New("bitrate / CRF must be a positive number")
		}
		if s.RC == "crf" && s.Rate > 51 {
			return errors.New("CRF must be between 0 and 51")
		}
		if s.Bits != 8 && s.Bits != 10 {
			return errors.New("bit depth must be 8 or 10")
		}
		if s.HW != "" {
			known := false
			for _, e := range hwEncoders {
				if e.id == s.HW {
					known = true
				}
			}
			if !known {
				return errors.New("unknown GPU encoder")
			}
			if s.VCodec == "libx264" && s.Bits == 10 {
				return errors.New("H.264 hardware encoders only support 8-bit - choose H.265 or 8-bit")
			}
		}
		if s.GOP < 0 || s.GOP > 100000 {
			return errors.New("invalid GOP length")
		}
		if s.Res != "source" && !resRe.MatchString(s.Res) {
			return errors.New("invalid resolution")
		}
		if s.FPS != "source" && !fpsRe.MatchString(s.FPS) {
			return errors.New("invalid frame rate")
		}
	}
	if !acodecs[s.ACodec] {
		return errors.New("unknown audio codec")
	}
	if s.ACodec != "flac" && (s.ABitrate < 8 || s.ABitrate > 1024) {
		return errors.New("audio bitrate must be between 8 and 1024 kbps")
	}
	return nil
}

// splitArgs splits "extra ffmpeg args" like a (very small) shell would.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case unicode.IsSpace(r):
			if has || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote in extra args")
	}
	if has || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args, nil
}

var (
	amixOnce sync.Once
	amixNorm bool
)

// amix got a 'normalize' option in FFmpeg 7; older builds scale volume by 1/N.
func amixHasNormalize() bool {
	amixOnce.Do(func() {
		cmd := exec.Command(ffmpegPath, "-hide_banner", "-h", "filter=amix")
		hideWindow(cmd)
		out, _ := cmd.CombinedOutput()
		amixNorm = strings.Contains(string(out), "normalize")
	})
	return amixNorm
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}

// graph is the input list plus the filter graph; export and the timeline
// player both build on it.
type graph struct {
	args   []string // global flags + "-ss/-t/-i" for every input
	filter string
	v, a   string // output labels, "" when absent
	total  float64
}

func (p *Project) graph() (*graph, error) {
	s := p.S
	n := len(p.V)

	useVAudio := false
	if !s.MuteV {
		for _, c := range p.V {
			if c.Audio {
				useVAudio = true
			}
		}
	}
	aclips := p.A
	if s.MuteA {
		aclips = nil
	}

	var vtotal, aend float64
	for _, c := range p.V {
		vtotal += c.dur()
	}
	for _, c := range aclips {
		if e := c.Offset + c.dur(); e > aend {
			aend = e
		}
	}
	total := vtotal
	if s.AudioOnly && aend > total {
		total = aend
	}

	args := []string{"-hide_banner", "-y", "-nostdin"}
	inputs := make([]Clip, 0, n+len(aclips))
	inputs = append(inputs, p.V...)
	inputs = append(inputs, aclips...)
	for _, c := range inputs {
		args = append(args, "-ss", ff3(c.Start), "-t", ff3(c.dur()), "-i", c.Path)
	}

	va, aa := !s.AudioOnly, useVAudio
	var W, H int
	fps, pf := "", "yuv420p"
	if va {
		if n == 0 || p.V[0].Video == nil {
			return nil, errors.New("the first video-lane clip has no video stream")
		}
		first := p.V[0].Video
		if s.Res == "source" {
			W, H = first.W, first.H
		} else {
			fmt.Sscanf(s.Res, "%dx%d", &W, &H)
		}
		W -= W % 2
		H -= H % 2
		if W <= 0 || H <= 0 {
			return nil, errors.New("could not determine output resolution")
		}
		if s.FPS == "source" {
			fps = first.FPS
		} else {
			fps = s.FPS
		}
		if !fpsRe.MatchString(fps) {
			fps = "30"
		}
		if s.Bits == 10 {
			pf = "yuv420p10le"
		}
	}

	var f []string
	var cat strings.Builder
	for i, c := range p.V {
		if va {
			f = append(f, fmt.Sprintf("[%d:v:0]scale=%d:%d:force_original_aspect_ratio=decrease,"+
				"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%s,format=%s,setpts=PTS-STARTPTS[v%d]",
				i, W, H, W, H, fps, pf, i))
			fmt.Fprintf(&cat, "[v%d]", i)
		}
		if aa {
			if c.Audio {
				f = append(f, fmt.Sprintf("[%d:a:0]aresample=48000,"+
					"aformat=sample_fmts=fltp:channel_layouts=stereo,asetpts=PTS-STARTPTS[a%d]", i, i))
			} else { // clip without audio -> silence of the same length
				f = append(f, fmt.Sprintf("anullsrc=r=48000:cl=stereo,atrim=0:%s,asetpts=PTS-STARTPTS[a%d]",
					ff3(c.dur()), i))
			}
			fmt.Fprintf(&cat, "[a%d]", i)
		}
	}
	if va || aa {
		outs := ""
		if va {
			outs += "[v]"
		}
		if aa {
			outs += "[ab]"
		}
		f = append(f, fmt.Sprintf("%sconcat=n=%d:v=%d:a=%d%s", cat.String(), n, b2i(va), b2i(aa), outs))
	}

	var labels []string
	if aa {
		labels = append(labels, "[ab]")
	}
	for j, c := range aclips {
		chain := fmt.Sprintf("[%d:a:0]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,"+
			"asetpts=PTS-STARTPTS", n+j)
		if ms := int(math.Round(c.Offset * 1000)); ms > 0 {
			chain += fmt.Sprintf(",adelay=delays=%d:all=1", ms)
		}
		f = append(f, chain+fmt.Sprintf("[al%d]", j))
		labels = append(labels, fmt.Sprintf("[al%d]", j))
	}
	alabel := ""
	switch {
	case len(labels) > 1:
		norm := ""
		if amixHasNormalize() {
			norm = ":normalize=0"
		}
		f = append(f, fmt.Sprintf("%samix=inputs=%d:duration=longest%s,alimiter=limit=0.97[aout]",
			strings.Join(labels, ""), len(labels), norm))
		alabel = "[aout]"
	case len(labels) == 1:
		alabel = labels[0]
	}

	g := &graph{args: args, filter: strings.Join(f, ";"), a: alabel, total: total}
	if va {
		g.v = "[v]"
	}
	return g, nil
}

// from returns the part of the timeline that starts at time t (seconds):
// clips before t are dropped, the clip under t is trimmed, audio offsets shift.
func (p *Project) from(t float64) *Project {
	q := &Project{S: p.S}
	acc := 0.0
	for _, c := range p.V {
		d := c.dur()
		if acc+d <= t+1e-6 {
			acc += d
			continue
		}
		if acc < t {
			c.Start += t - acc
		}
		acc += d
		if c.dur() >= 0.05 {
			q.V = append(q.V, c)
		}
	}
	for _, c := range p.A {
		if c.Offset+c.dur() <= t+1e-6 {
			continue
		}
		if c.Offset >= t {
			c.Offset -= t
		} else {
			c.Start += t - c.Offset
			c.Offset = 0
		}
		if c.dur() >= 0.05 {
			q.A = append(q.A, c)
		}
	}
	return q
}

// build returns the ffmpeg argument lists (one per pass) and the total duration.
func (p *Project) build(out, logdir string) ([][]string, float64, error) {
	g, err := p.graph()
	if err != nil {
		return nil, 0, err
	}
	s := p.S
	if s.AudioOnly && g.a == "" {
		return nil, 0, errors.New("no audio to export: add audio clips or unmute the video lane")
	}

	args := append(append([]string{}, g.args...), "-filter_complex", g.filter)
	if g.v != "" {
		args = append(args, "-map", g.v)
	}
	if g.a != "" {
		args = append(args, "-map", g.a)
	}

	var vargs []string
	if g.v != "" {
		if s.HW != "" {
			vargs = hwArgs(&s) // GPU: vendor-specific options (no tune, no 2-pass)
		} else {
			vargs = []string{"-c:v", s.VCodec, "-preset", s.Preset}
			if s.Tune != "none" {
				vargs = append(vargs, "-tune", s.Tune)
			}
			rate := strconv.FormatFloat(s.Rate, 'f', -1, 64)
			if s.RC == "crf" {
				vargs = append(vargs, "-crf", rate)
			} else {
				vargs = append(vargs, "-b:v", rate+"k")
			}
		}
		if s.GOP > 0 {
			vargs = append(vargs, "-g", strconv.Itoa(s.GOP))
		}
		extra, err := splitArgs(s.Extra)
		if err != nil {
			return nil, 0, err
		}
		vargs = append(vargs, extra...)
	}
	var aargs []string
	if g.a != "" {
		aargs = []string{"-c:a", s.ACodec}
		if s.ACodec != "flac" {
			aargs = append(aargs, "-b:a", strconv.Itoa(s.ABitrate)+"k")
		}
	}
	tail := []string{"-t", ff3(g.total), "-progress", "pipe:1", "-nostats"}

	join := func(parts ...[]string) []string {
		var r []string
		for _, part := range parts {
			r = append(r, part...)
		}
		return r
	}
	if g.v != "" && s.HW == "" && s.RC == "bitrate" && s.TwoPass {
		log := filepath.Join(logdir, "pass")
		p1 := join(args, vargs, []string{"-pass", "1", "-passlogfile", log}, aargs, tail,
			[]string{"-f", "null", nullDevice()})
		p2 := join(args, vargs, []string{"-pass", "2", "-passlogfile", log}, aargs, tail, []string{out})
		return [][]string{p1, p2}, g.total, nil
	}
	return [][]string{join(args, vargs, aargs, tail, []string{out})}, g.total, nil
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, e1 := os.Stat(a)
	sb, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(sa, sb)
}

// ---- export runner -----------------------------------------------------------

type jobResult struct {
	cancelled bool
	err       error
}

// runJob runs the ffmpeg passes one after another, reporting progress through
// onProg. Cancelling ctx kills ffmpeg. tmp is removed when finished.
func runJob(ctx context.Context, cmds [][]string, total float64, out, tmp string,
	onProg func(pct float64, text string)) jobResult {

	defer os.RemoveAll(tmp)
	var tail []string
	var runErr error
	wroteOut := false
	speed := ""

	for i, args := range cmds {
		if i == len(cmds)-1 {
			wroteOut = true
		}
		cmd := newCmd(ctx, ffmpegPath, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			runErr = err
			break
		}
		cmd.Stderr = cmd.Stdout // one merged stream: progress + log lines
		if err := cmd.Start(); err != nil {
			runErr = err
			break
		}
		// If cancelling cannot stop ffmpeg (or something it started) quickly,
		// closing our end of the pipe still frees the reader below.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				time.Sleep(2 * time.Second)
				stdout.Close()
			case <-done:
			}
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r\n ")
			m := progRe.FindStringSubmatch(line)
			if m == nil {
				tail = append(tail, line)
				if len(tail) > 20 {
					tail = tail[1:]
				}
				continue
			}
			switch m[1] {
			case "speed":
				speed = m[2]
			case "out_time_us":
				us, perr := strconv.ParseInt(m[2], 10, 64)
				if perr != nil || us < 0 {
					continue
				}
				frac := math.Min(1, float64(us)/1e6/math.Max(total, 0.001))
				pct := (float64(i) + frac) / float64(len(cmds)) * 100
				pre := ""
				if len(cmds) > 1 {
					pre = fmt.Sprintf("Pass %d/%d · ", i+1, len(cmds))
				}
				onProg(pct, fmt.Sprintf("%s%.0f%%  %s", pre, pct, speed))
			}
		}
		io.Copy(io.Discard, stdout) // never leave ffmpeg blocked on a full pipe
		close(done)
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			break
		}
		if waitErr != nil {
			msg := strings.Join(tail, "\n")
			if msg == "" {
				msg = waitErr.Error()
			}
			runErr = errors.New(msg)
			break
		}
	}

	cancelled := ctx.Err() != nil
	if (cancelled || runErr != nil) && wroteOut {
		os.Remove(out) // never leave a half-written file behind
	}
	return jobResult{cancelled: cancelled, err: runErr}
}
