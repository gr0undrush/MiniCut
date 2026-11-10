package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"github.com/ncruces/zenity"
)

// File dialogs. MiniCut uses the operating system's own dialogs (Windows
// common dialogs, macOS, and zenity / kdialog on Linux) through the zenity
// package. If the system dialog cannot be shown (for example zenity is not
// installed on Linux) the dialogs built into Fyne are used instead.

// mediaPatterns lists "*.mp4", "*.MP4", ... (some systems match case-sensitively).
func mediaPatterns() []string {
	var p []string
	for _, e := range mediaExts {
		p = append(p, "*"+e, "*"+strings.ToUpper(e))
	}
	return p
}

func (a *App) startDir() string { return a.fa.Preferences().String("dir") }

// pickOpenMulti shows the system "open" dialog (several files can be chosen).
func (a *App) pickOpenMulti(cb func([]string)) {
	go func() {
		opts := []zenity.Option{
			zenity.Title("Import media"),
			zenity.FileFilters{
				{Name: "Media files", Patterns: mediaPatterns()},
				{Name: "All files", Patterns: []string{"*"}},
			},
		}
		if d := a.startDir(); d != "" {
			opts = append(opts, zenity.Filename(d+string(os.PathSeparator)))
		}
		paths, err := zenity.SelectFileMultiple(opts...)
		switch {
		case err == nil:
			if len(paths) > 0 {
				a.rememberDir(filepath.Dir(paths[0]))
				cb(paths)
			}
		case errors.Is(err, zenity.ErrCanceled):
			// the user closed the dialog
		default:
			a.fyneOpen(cb) // no system dialog available
		}
	}()
}

// pickSave shows the system "save as" dialog; cb gets the chosen path with the
// extension that fits the mode (.mkv, or .mka for audio only).
func (a *App) pickSave(cb func(string)) {
	ext := ".mkv"
	if a.mode.Selected == "Audio only" {
		ext = ".mka"
	}
	go func() {
		name := "output" + ext
		if d := a.startDir(); d != "" {
			name = filepath.Join(d, name)
		}
		p, err := zenity.SelectFileSave(
			zenity.Title("Save as"),
			zenity.ConfirmOverwrite(), // the system dialog asks before replacing a file
			zenity.Filename(name),
			zenity.FileFilters{{Name: "Matroska (" + ext + ")", Patterns: []string{"*" + ext}}},
		)
		switch {
		case err == nil && p != "":
			p = strings.TrimSuffix(p, filepath.Ext(p)) + ext
			a.savedPath = p // already confirmed: do not ask again when exporting
			a.rememberDir(filepath.Dir(p))
			cb(p)
		case err == nil || errors.Is(err, zenity.ErrCanceled):
			// cancelled
		default:
			a.fyneSave(cb, ext)
		}
	}()
}

// ---- fallback: Fyne's own dialogs ----------------------------------------------

func (a *App) fyneOpen(cb func([]string)) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if r == nil {
			return
		}
		p := uriPath(r.URI())
		r.Close()
		a.rememberDir(filepath.Dir(p))
		cb([]string{p})
	}, a.win)
	d.SetFilter(storage.NewExtensionFileFilter(mediaExts))
	a.setDialogDir(d)
	d.Resize(fyne.NewSize(860, 580))
	d.Show()
}

func (a *App) fyneSave(cb func(string), ext string) {
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if wc == nil {
			return
		}
		p := uriPath(wc.URI())
		wc.Close()
		a.rememberDir(filepath.Dir(p))
		cb(p)
	}, a.win)
	d.SetFileName("output" + ext)
	d.SetFilter(storage.NewExtensionFileFilter([]string{ext}))
	a.setDialogDir(d)
	d.Resize(fyne.NewSize(860, 580))
	d.Show()
}
