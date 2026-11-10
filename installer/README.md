# Windows installer

Builds a one-file installer that installs MiniCut **and ffmpeg** (ffmpeg.exe, ffprobe.exe, ffplay.exe are
put next to minicut.exe; MiniCut prefers that copy over any ffmpeg in PATH), plus a portable zip.

## Build it
Prerequisites: Go + MinGW (see the main README) and [Inno Setup 6](https://jrsoftware.org/isinfo.php).

```bat
installer\build-installer.bat
```

It runs `build.bat`, downloads ffmpeg once into `installer\ffmpeg\` (`get-ffmpeg.ps1`), compiles
`minicut.iss` and writes to `dist\`:

- `MiniCut-Setup-<version>.exe` - installer (per-user install, no administrator prompt; choose "all users" in the first dialog if you want)
- `MiniCut-portable.zip` - unzip and run, no installation

Another version number: `installer\build-installer.bat /DAppVersion=1.2.0`.

## Release on GitHub
Pushing a tag such as `v1.1.0` runs `.github/workflows/release.yml`, which builds the installer on a
Windows runner and attaches both files to the release.

## FFmpeg license
The ffmpeg build that gets bundled is GPLv3. If you hand out the installer you are redistributing it:
keep the license files (the installer already copies them to `licenses\`) and make the source available
(https://ffmpeg.org/download.html#get-sources). To use a different ffmpeg build, put its three .exe files and a
`LICENSE.txt` into `installer\ffmpeg\` before running the script.
