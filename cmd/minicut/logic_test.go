package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPath() string { return filepath.Join(os.TempDir(), "a.mp4") }

func testClip(start, end float64) Clip {
	return Clip{Path: testPath(), Start: start, End: end, Dur: 60, Audio: true,
		Video: &VideoInfo{W: 1920, H: 1080, FPS: "25/1"}}
}

func baseSettings() Settings {
	return Settings{VCodec: "libx264", RC: "crf", Rate: 20, Preset: "medium", Tune: "none", Bits: 8,
		Res: "source", FPS: "source", ACodec: "aac", ABitrate: 128}
}

func hasPair(args []string, key, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestParseT(t *testing.T) {
	cases := map[string]float64{"90": 90, "1:30": 90, "01:02:03.5": 3723.5, "1,5": 1.5}
	for in, want := range cases {
		got, err := parseT(in)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("parseT(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseT("abc"); err == nil {
		t.Error(`parseT("abc") should fail`)
	}
}

func TestFmtT(t *testing.T) {
	if got := fmtT(0); got != "00:00:00.000" {
		t.Errorf("fmtT(0) = %q", got)
	}
	if got := fmtT(3723.5); got != "01:02:03.500" {
		t.Errorf("fmtT(3723.5) = %q", got)
	}
}

func TestFmtShort(t *testing.T) {
	if got := fmtShort(75, false); got != "1:15" {
		t.Errorf("fmtShort(75) = %q", got)
	}
	if got := fmtShort(3725, false); got != "1:02:05" {
		t.Errorf("fmtShort(3725) = %q", got)
	}
	if got := fmtShort(75.5, true); got != "1:15.5" {
		t.Errorf("fmtShort(75.5, true) = %q", got)
	}
}

func TestSplitArgs(t *testing.T) {
	got, err := splitArgs(`-a "b c" 'd e' f`)
	want := []string{"-a", "b c", "d e", "f"}
	if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitArgs = %q, %v; want %q", got, err, want)
	}
	if _, err := splitArgs(`"unclosed`); err == nil {
		t.Error("an unclosed quote should be an error")
	}
}

func TestCapFPS(t *testing.T) {
	if got := capFPS("30000/1001"); got < 29.9 || got > 30 {
		t.Errorf("capFPS(30000/1001) = %v", got)
	}
	if got := capFPS("60"); got != 30 {
		t.Errorf("capFPS(60) = %v, want 30", got)
	}
	if got := capFPS("0/0"); got != 25 {
		t.Errorf("capFPS(0/0) = %v, want 25", got)
	}
}

func TestFrom(t *testing.T) {
	p := &Project{
		V: []Clip{testClip(0, 10), testClip(0, 10)},
		A: []Clip{
			{Path: testPath(), Start: 0, End: 4, Offset: 12, Audio: true},
			{Path: testPath(), Start: 0, End: 20, Offset: 0, Audio: true},
			{Path: testPath(), Start: 0, End: 3, Offset: 0, Audio: true},
		},
	}
	q := p.from(15)
	if len(q.V) != 1 || q.V[0].Start != 5 || q.V[0].End != 10 {
		t.Errorf("from(15) video = %+v", q.V)
	}
	q = p.from(10)
	if len(q.V) != 1 || q.V[0].Start != 0 {
		t.Errorf("from(10) video = %+v", q.V)
	}
	if len(q.A) != 2 || q.A[0].Offset != 2 || q.A[1].Start != 10 || q.A[1].Offset != 0 {
		t.Errorf("from(10) audio = %+v", q.A)
	}
}

func TestValidate(t *testing.T) {
	if err := (&Project{S: baseSettings()}).validate(); err == nil {
		t.Error("an empty timeline must be rejected")
	}
	ok := &Project{V: []Clip{testClip(0, 5)}, S: baseSettings()}
	if err := ok.validate(); err != nil {
		t.Errorf("valid project rejected: %v", err)
	}
	bad := &Project{V: []Clip{testClip(0, 5)}, S: baseSettings()}
	bad.S.HW, bad.S.Bits = "nvenc", 10 // 10-bit needs H.265 on hardware encoders
	if err := bad.validate(); err == nil {
		t.Error("H.264 + 10-bit on a GPU encoder must be rejected")
	}
	bad = &Project{V: []Clip{testClip(0, 5)}, S: baseSettings()}
	bad.S.Preset = "turbo"
	if err := bad.validate(); err == nil {
		t.Error("an unknown preset must be rejected")
	}
}

func TestGraphVideoAndAudio(t *testing.T) {
	p := &Project{V: []Clip{testClip(0, 5)}, S: baseSettings()}
	p.S.Res, p.S.FPS = "1280x720", "30"
	g, err := p.graph()
	if err != nil {
		t.Fatal(err)
	}
	if g.v != "[v]" || g.a != "[ab]" || math.Abs(g.total-5) > 1e-9 {
		t.Errorf("labels/total = %q %q %v", g.v, g.a, g.total)
	}
	if !strings.Contains(g.filter, "scale=1280:720:force_original_aspect_ratio=decrease") ||
		!strings.Contains(g.filter, "concat=n=1:v=1:a=1[v][ab]") {
		t.Errorf("unexpected filter graph: %s", g.filter)
	}
	if !hasPair(g.args, "-t", "5.000") {
		t.Errorf("input length missing: %v", g.args)
	}

	p.S.AudioOnly = true
	if g, _ = p.graph(); g.v != "" || g.a != "[ab]" {
		t.Errorf("audio only: v=%q a=%q", g.v, g.a)
	}
	p.S.AudioOnly, p.S.MuteV = false, true
	if g, _ = p.graph(); g.a != "" {
		t.Errorf("muted video lane should leave no audio, got %q", g.a)
	}
}

func TestGraphMixesAudioLane(t *testing.T) {
	p := &Project{
		V: []Clip{testClip(0, 5)},
		A: []Clip{{Path: testPath(), Start: 0, End: 3, Offset: 1.5, Audio: true}},
		S: baseSettings(),
	}
	g, err := p.graph()
	if err != nil {
		t.Fatal(err)
	}
	if g.a != "[aout]" || !strings.Contains(g.filter, "adelay=delays=1500:all=1") ||
		!strings.Contains(g.filter, "amix=inputs=2:duration=longest") {
		t.Errorf("unexpected mix graph: %s (a=%q)", g.filter, g.a)
	}
}

func TestBuildTwoPass(t *testing.T) {
	p := &Project{V: []Clip{testClip(0, 5)}, S: baseSettings()}
	p.S.RC, p.S.Rate, p.S.TwoPass = "bitrate", 4000, true
	out := filepath.Join(os.TempDir(), "o.mkv")
	cmds, total, err := p.build(out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || math.Abs(total-5) > 1e-9 {
		t.Fatalf("passes = %d, total = %v", len(cmds), total)
	}
	if !hasPair(cmds[0], "-pass", "1") || !hasPair(cmds[1], "-pass", "2") || !hasPair(cmds[1], "-b:v", "4000k") {
		t.Errorf("pass arguments wrong:\n%v\n%v", cmds[0], cmds[1])
	}
	if cmds[0][len(cmds[0])-1] != nullDevice() || cmds[1][len(cmds[1])-1] != out {
		t.Error("pass 1 must write to the null device and pass 2 to the output file")
	}
}

func TestHardwareArgs(t *testing.T) {
	s := Settings{HW: "nvenc", VCodec: "libx265", RC: "crf", Rate: 23.4, Preset: "medium", Bits: 10}
	args := hwArgs(&s)
	if len(args) < 2 || args[1] != "hevc_nvenc" {
		t.Fatalf("encoder = %v", args)
	}
	for _, kv := range [][2]string{{"-preset", "p5"}, {"-rc", "vbr"}, {"-cq", "23"},
		{"-pix_fmt", "p010le"}, {"-profile:v", "main10"}} {
		if !hasPair(args, kv[0], kv[1]) {
			t.Errorf("missing %s %s in %v", kv[0], kv[1], args)
		}
	}
	if nvencPreset("ultrafast") != "p1" || nvencPreset("veryslow") != "p7" {
		t.Error("nvenc preset mapping")
	}
}

func TestSplit(t *testing.T) {
	a := &App{
		vclips: []Clip{testClip(0, 10), testClip(0, 10)},
		aclips: []Clip{{Path: testPath(), Start: 0, End: 8, Offset: 2, Audio: true}},
	}
	if a.splitVideo(0, 10) { // exactly on the join: nothing to split
		t.Error("splitting on a clip boundary must be refused")
	}
	if !a.splitVideo(1, 15) { // second clip spans 10..20 on the timeline
		t.Fatal("split inside the second clip was refused")
	}
	if len(a.vclips) != 3 || a.vclips[1].End != 5 || a.vclips[2].Start != 5 || a.vclips[2].End != 10 {
		t.Errorf("video clips after split: %+v", a.vclips)
	}
	if !a.splitAudio(0, 6) { // audio clip spans 2..10
		t.Fatal("audio split was refused")
	}
	if len(a.aclips) != 2 || a.aclips[0].End != 4 || a.aclips[1].Start != 4 || a.aclips[1].Offset != 6 || a.aclips[1].End != 8 {
		t.Errorf("audio clips after split: %+v", a.aclips)
	}
}
