# Contributing

Thanks for helping! Bug reports, ideas and pull requests are welcome.

## Set up
1. Install Go 1.21+, a C compiler and ffmpeg (see the README, section "Requirements").
2. `go mod tidy` (first time), then `./build.sh` (Linux/macOS) or `build.bat` (Windows).
3. `go test ./...` runs the unit tests (project model, command builder, time parsing).

## Before you open a pull request
- Run `gofmt -w .` and `go build ./...` and `go test ./...`.
- Keep ffmpeg arguments in `cmd/minicut/encode.go` / `hw.go`; every value that reaches the
  command line is validated in `Project.validate`.
- Anything that starts a long-running process must use `newCmd` (`procutil.go`) so Cancel
  can stop the whole process tree.
- Describe what you tested (OS, GPU, ffmpeg version) in the pull request.

The Windows installer is described in `installer/README.md`.
See `docs/ARCHITECTURE.md` for how the code is organised.
