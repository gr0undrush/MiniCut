package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

var ffmpegPath, ffprobePath, ffplayPath string

// lookTool finds a tool in PATH, or next to the MiniCut executable.
func lookTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), name)
		if runtime.GOOS == "windows" {
			cand += ".exe"
		}
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

type VideoInfo struct {
	W   int
	H   int
	FPS string
}

type Media struct {
	Path     string
	Name     string
	Duration float64
	Video    *VideoInfo
	Audio    bool
}

type ffprobeOut struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		RFrameRate  string `json:"r_frame_rate"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideDataList []struct {
			Rotation interface{} `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
}

func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func ff3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func probeMedia(path string) (*Media, error) {
	if ffprobePath == "" {
		return nil, errors.New("ffprobe not found")
	}
	cmd := exec.Command(ffprobePath, "-v", "error", "-show_entries",
		"format=duration:stream=codec_type,width,height,r_frame_rate:stream_side_data=rotation:stream_disposition=attached_pic",
		"-of", "json", path)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read %s", filepath.Base(path))
	}
	var po ffprobeOut
	if err := json.Unmarshal(out, &po); err != nil {
		return nil, err
	}
	m := &Media{Path: path, Name: filepath.Base(path)}
	m.Duration, _ = strconv.ParseFloat(po.Format.Duration, 64)
	for _, s := range po.Streams {
		switch s.CodecType {
		case "video":
			if m.Video != nil || s.Disposition.AttachedPic == 1 {
				continue // first real video stream only; skip cover art
			}
			w, h := s.Width, s.Height
			rot := 0.0
			for _, sd := range s.SideDataList {
				if r := toFloat(sd.Rotation); r != 0 {
					rot = r
				}
			}
			if r := int(math.Abs(rot)) % 360; r == 90 || r == 270 {
				w, h = h, w
			}
			fps := s.RFrameRate
			if fps == "" || fps == "0/0" {
				fps = "30"
			}
			m.Video = &VideoInfo{W: w, H: h, FPS: fps}
		case "audio":
			m.Audio = true
		}
	}
	return m, nil
}

// grabFrame decodes one preview frame; cancelling ctx kills ffmpeg (fast scrubbing).
func grabFrame(ctx context.Context, path string, t float64) (image.Image, error) {
	if ffmpegPath == "" {
		return nil, errors.New("ffmpeg not found")
	}
	cmd := newCmd(ctx, ffmpegPath, "-v", "error", "-ss", ff3(t), "-i", path,
		"-frames:v", "1", "-vf", "scale=w=960:h=540:force_original_aspect_ratio=decrease",
		"-f", "image2pipe", "-vcodec", "png", "-")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, errors.New("no frame")
	}
	return png.Decode(bytes.NewReader(out))
}
