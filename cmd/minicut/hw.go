package main

import (
	"context"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hwEncoder describes one family of hardware video encoders in ffmpeg.
type hwEncoder struct {
	id    string // stored in Settings.HW
	label string // shown in the UI as "GPU: <label>"
	h264  string
	h265  string
}

var hwEncoders = []hwEncoder{
	{"nvenc", "NVIDIA NVENC", "h264_nvenc", "hevc_nvenc"},
	{"qsv", "Intel Quick Sync", "h264_qsv", "hevc_qsv"},
	{"amf", "AMD AMF", "h264_amf", "hevc_amf"},
	{"videotoolbox", "Apple VideoToolbox", "h264_videotoolbox", "hevc_videotoolbox"},
}

const cpuEncoderLabel = "CPU (x264 / x265)"

var (
	hwMu    sync.RWMutex
	hwAvail = map[string]bool{} // ffmpeg encoder name -> works on this machine
)

func hwOK(name string) bool {
	hwMu.RLock()
	defer hwMu.RUnlock()
	return hwAvail[name]
}

// hwEncoderName returns the ffmpeg encoder for a GPU family and codec.
func hwEncoderName(id, vcodec string) string {
	for _, e := range hwEncoders {
		if e.id == id {
			if vcodec == "libx265" {
				return e.h265
			}
			return e.h264
		}
	}
	return ""
}

// detectHW finds which hardware encoders really work: the encoder must be in
// this ffmpeg build AND a tiny test encode must succeed (this fails when the
// GPU or driver is missing). It returns the GPU labels that can be offered.
func detectHW() []string {
	if ffmpegPath == "" {
		return nil
	}
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-encoders")
	hideWindow(cmd)
	out, _ := cmd.Output()
	list := string(out)

	var wg sync.WaitGroup
	for _, e := range hwEncoders {
		for _, name := range []string{e.h264, e.h265} {
			if !strings.Contains(list, name) {
				continue
			}
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				ok := testEncoder(name)
				hwMu.Lock()
				hwAvail[name] = ok
				hwMu.Unlock()
			}(name)
		}
	}
	wg.Wait()

	var labels []string
	for _, e := range hwEncoders {
		if hwOK(e.h264) || hwOK(e.h265) {
			labels = append(labels, "GPU: "+e.label)
		}
	}
	return labels
}

func testEncoder(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-v", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:r=25:d=0.5",
		"-frames:v", "5", "-c:v", name, "-f", "null", "-")
	hideWindow(cmd)
	return cmd.Run() == nil
}

// ---- preset mapping -----------------------------------------------------------

func nvencPreset(p string) string { // p1 (fastest) .. p7 (best); needs ffmpeg 4.4+
	switch p {
	case "ultrafast":
		return "p1"
	case "superfast":
		return "p2"
	case "veryfast", "faster":
		return "p3"
	case "fast":
		return "p4"
	case "medium":
		return "p5"
	case "slow":
		return "p6"
	}
	return "p7"
}

func qsvPreset(p string) string {
	if p == "ultrafast" || p == "superfast" {
		return "veryfast"
	}
	return p
}

func amfQuality(p string) string {
	switch p {
	case "ultrafast", "superfast", "veryfast":
		return "speed"
	case "faster", "fast", "medium":
		return "balanced"
	}
	return "quality"
}

// hwArgs returns the video-encoder arguments for a hardware encoder.
// CRF-style quality is mapped to each vendor's constant-quality mode.
func hwArgs(s *Settings) []string {
	args := []string{"-c:v", hwEncoderName(s.HW, s.VCodec)}
	crf := s.RC == "crf"
	ten := s.Bits == 10
	bitrate := strconv.FormatFloat(s.Rate, 'f', -1, 64) + "k"
	q := int(math.Round(s.Rate))
	if q < 1 {
		q = 1
	}
	if q > 51 {
		q = 51
	}
	qs := strconv.Itoa(q)

	pix := func(eight, tenBit string) {
		if ten {
			args = append(args, "-pix_fmt", tenBit)
		} else {
			args = append(args, "-pix_fmt", eight)
		}
	}

	switch s.HW {
	case "nvenc":
		args = append(args, "-preset", nvencPreset(s.Preset))
		if crf {
			args = append(args, "-rc", "vbr", "-cq", qs, "-b:v", "0")
		} else {
			args = append(args, "-rc", "vbr", "-b:v", bitrate)
		}
		pix("yuv420p", "p010le")
		if ten {
			args = append(args, "-profile:v", "main10")
		}
	case "qsv":
		args = append(args, "-preset", qsvPreset(s.Preset))
		if crf {
			args = append(args, "-global_quality", qs)
		} else {
			args = append(args, "-b:v", bitrate)
		}
		pix("nv12", "p010le")
		if ten {
			args = append(args, "-profile:v", "main10")
		}
	case "amf":
		args = append(args, "-quality", amfQuality(s.Preset))
		if crf {
			args = append(args, "-rc", "cqp", "-qp_i", qs, "-qp_p", qs)
		} else {
			args = append(args, "-rc", "vbr_peak", "-b:v", bitrate)
		}
		pix("nv12", "p010le")
	case "videotoolbox":
		if crf { // VideoToolbox quality is 1..100 (higher = better)
			vq := int(math.Round(100 - s.Rate*2))
			if vq < 1 {
				vq = 1
			}
			if vq > 100 {
				vq = 100
			}
			args = append(args, "-q:v", strconv.Itoa(vq))
		} else {
			args = append(args, "-b:v", bitrate)
		}
		pix("yuv420p", "p010le")
	}
	return args
}
