package app

import (
	"github.com/egoist/mygo"

	"github.com/lbr77/lody-mygo/locales"
)

// Closing the window or quitting asks first while work would be lost, as
// Lody's desktop app does: messages still waiting on their attachments,
// and a file's unsaved edits. Lody's native sheet offers Stay, the
// default, and Leave, or Quit when the app is quitting; a quit asks once,
// not again for the window it closes.

// unsavedWork reports whether leaving now would lose something.
func (a *App) unsavedWork() bool {
	if len(a.pending.list) > 0 {
		return true
	}
	if in := a.inspect; in != nil && in.preview != nil {
		fp := in.preview
		return fp.editing && fp.code != nil && fp.code.Buffer.Modified()
	}
	return false
}

// confirmUnload is Lody's unload sheet: whether to go ahead.
var confirmUnload = func(win *mygo.Window, quitting bool) bool {
	leave := locales.UnloadLeave()
	if quitting {
		leave = locales.UnloadQuit()
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:        win,
		Type:          mygo.MessageWarning,
		Message:       locales.UnloadTitle(),
		Detail:        locales.UnloadDetail(),
		Buttons:       []string{locales.UnloadStay(), leave},
		DefaultButton: 0,
		CancelButton:  0,
	})
	return err == nil && res.Button == 1
}

// guardUnload asks before win closes or the app quits while work would be
// lost.
func (a *App) guardUnload(win *mygo.Window) {
	leaving := false // the user chose to leave this time
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if leaving || !a.unsavedWork() {
			return
		}
		if confirmUnload(win, true) {
			leaving = true
			return
		}
		e.PreventDefault()
	})
	win.OnClose(func(e *mygo.CloseEvent) {
		if leaving || !a.unsavedWork() {
			return
		}
		if !confirmUnload(win, false) {
			e.PreventDefault()
		}
	})
}
