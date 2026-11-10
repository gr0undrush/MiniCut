# Changelog

## 1.1.0
- **Split**: the clip at the playhead is cut into two clips. Buttons *Split* (next to *Play timeline*) and
  *Split at playhead*, key `S`. A selected clip is cut if the playhead is inside it, otherwise all clips under the playhead.
- System file dialogs (Windows / macOS / zenity or kdialog on Linux) for import (multi-select) and *Save as*;
  the built-in dialogs remain as a fallback.
- Media library and Export are two cards of the same size (50/50); the export settings use two columns.
- A copy of ffmpeg next to the executable is preferred over the one in PATH.
- Windows installer that includes ffmpeg, portable zip, and a GitHub release workflow.

## 1.0.0
- Native desktop app (Go + Fyne), ffmpeg does all media work.
- Media library with Video and Audio folders; import by dialog or by dropping files on the window.
- Two-lane timeline: drag items in from the library, trim by dragging clip edges, move clips,
  split at the playhead, mute lanes, free placement on the audio lane, audio mixed over video sound.
- Media monitor (preview of library items) and Timeline player (plays the cut result with sound).
- Export to Matroska (.mkv / .mka): H.264 or H.265, bitrate or CRF, 2-pass, preset, tune, bit depth,
  GOP, resolution, frame rate, AAC / Opus / FLAC, extra ffmpeg arguments.
- GPU encoding: NVIDIA NVENC, Intel Quick Sync, AMD AMF, Apple VideoToolbox (auto-detected).
- Cancel stops ffmpeg together with everything it started.
- Responsive layout (monitors grow with the window), timeline fit mode (a 3-hour project fits
  without a scroll bar), UI size menu, F11 full screen.
