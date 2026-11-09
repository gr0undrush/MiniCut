# Architecture

MiniCut is one Go `main` package (`cmd/minicut`) on top of the Fyne GUI toolkit. All media work is done by
`ffmpeg` / `ffprobe` / `ffplay` child processes; there is no media code in Go.

## Data model (`encode.go`)

| Type | Meaning |
|------|---------|
| `Clip` | one piece of a source file: `Path`, `Start`/`End` (seconds in the source), `Dur` (length of the whole source, the limit for trimming), `Offset` (audio lane only: position on the timeline), `Audio`, `Video` |
| `Settings` | everything from the export panel (codec, rate control, GPU encoder `HW`, mutes, ...) |
| `Project` | `V` (video lane), `A` (audio lane), `S` (settings) |

Timeline semantics:

- **Video lane**: clips play one after another. A clip's position is the sum of the lengths before it, so
  trimming and reordering "ripple". There are no gaps.
- **Audio lane**: each clip has an explicit `Offset`; gaps and overlaps are allowed.
- The audio of the video-lane clips and the audio lane are mixed, unless a lane is muted.

The `App` struct (`main.go`) owns the lists `vclips` / `aclips`, the library (`libV`, `libA`) and the widgets.
`App.changed()` is the one place that refreshes everything after the timeline was edited.

## Building the ffmpeg command (`encode.go`)

`Project.graph()` produces the part shared by export and preview: the inputs (`-ss start -t length -i file` for
every clip) and the filter graph.

```
[0:v:0]scale=W:H,pad=...,setsar=1,fps=F,format=...,setpts=PTS-STARTPTS[v0]   one chain per video clip
[0:a:0]aresample=48000,aformat=...,asetpts=PTS-STARTPTS[a0]                  (silence via anullsrc if no audio)
[v0][a0][v1][a1]...concat=n=N:v=1:a=1[v][ab]                                 joins the video lane
[N:a:0]...,adelay=<offset ms>:all=1[al0]                                     one chain per audio-lane clip
[ab][al0]...amix=inputs=K:duration=longest[:normalize=0],alimiter[aout]      mix
```

`Project.build()` adds the encoder options (`libx264`/`libx265` or a hardware encoder from `hw.go`), the audio
codec and `-progress pipe:1`; with 2-pass it returns two commands. `Project.validate()` checks every value that ends
up on the command line (the UI is not trusted to be consistent). `Project.from(t)` returns the timeline starting at
time `t`; the players use it to start playback anywhere.

## Timeline widget (`timeline.go`)

A custom Fyne widget (`widget.BaseWidget` with its own renderer) that is rebuilt from the clip lists on every
refresh. It implements Fyne's mouse, drag, double-tap, hover/cursor and focus interfaces:

- `MouseDown` decides what a drag will do: seek (ruler / empty space), trim left or right (within `tlEdge` pixels
  of a clip's end) or move the clip.
- Trimming edits the clip live (`applyTrim`); moving a video clip draws a ghost and reorders on release; moving an
  audio clip shows a candidate offset and snaps to edit points (`snapPoint`).
- Library items are dragged by `libItem` (`library.go`); Fyne has no drag and drop between widgets, so the item
  reports the window position to `Timeline.libDrag` / `libDrop`, which converts it to timeline coordinates.
- **Fit mode** (`fitMode`, default): the renderer computes `pps` (pixels per second) from the width it is given, and
  reports a tiny `MinSize`, so the content is never wider than the window and the scroll bar never appears. Manual
  zoom switches fit mode off; the Fit button switches it on.

## Players (`player.go`)

Two monitors, same technique:

- **Picture**: an ffmpeg process writes raw RGBA frames to a pipe; Go reads one frame at a time, shows it in a
  `canvas.Image` and moves the slider. The media monitor lets ffmpeg pace itself (`-re`); the timeline player
  paces the frames in Go because its input is a filter graph.
- **Sound**: `ffplay -nodisp` for a single file, or `ffmpeg ... -f wav -` piped into a window-less `ffplay` for the
  mixed timeline audio. Picture and sound are separate processes, so long previews can drift slightly.
- Scrubbing grabs a single PNG frame (`grabFrame`, `grabTimelineFrame`); stale requests are cancelled through their
  context.

`playState` tracks one player and uses a generation counter so a finished player cannot reset a newer one.

## Child processes (`procutil.go`, `proc_*.go`)

Always start long-running tools with `newCmd`. It hides the console window on Windows and makes cancelling kill the
whole process tree (`taskkill /T` on Windows, a process group elsewhere). This matters because `ffmpeg.exe` in
`PATH` is often only a launcher (Chocolatey and Scoop shims): killing the launcher alone would leave the real
ffmpeg encoding.

## GPU encoders (`hw.go`)

At start-up a goroutine runs `ffmpeg -encoders` and a half-second test encode for every hardware encoder found.
Only encoders that really work are offered. `hwArgs` maps the generic settings (preset, CRF/bitrate, bit depth) to
the options of each vendor.

## Layout (`layouts.go`)

`pageLayout` gives the timeline and the bottom row exactly the height they need and the monitors everything else;
`aspectLayout` keeps a monitor at 16:9; `splitLayout` / `capLayout` size the bottom row and the export controls.

## Notes for contributors

- Built against Fyne 2.5. UI updates from goroutines are allowed in that version; if you upgrade to Fyne 2.6 or
  newer, wrap them in `fyne.Do` / `fyne.DoAndWait`.
- The preview size is `pvW x pvH` in `player.go`.
- Ideas: gaps on the video lane (black filler), video/audio linked clips, undo/redo, project save/load,
  transitions, hardware decoding for previews, keyframe-accurate stream-copy export.
