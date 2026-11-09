// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package webcanvas

import (
	"sync/atomic"
	"syscall/js"
)

// pageClipboard is the bridge paste shortcuts wait for; nil until NewClipboard.
var pageClipboard atomic.Pointer[Clipboard]

// swallow is the rejection handler of writeText: a refused write (no user
// activation, a denied permission) leaves the in-page text, which still pastes
// inside the page. Never released: it lives as long as the page.
var swallow = js.FuncOf(func(js.Value, []js.Value) any { return nil })

// NewClipboard returns the page's clipboard bridge and makes it the one every
// surface's paste shortcut waits for (see clipboard.go). Install it in the
// widget toolkit once at startup:
//
//	toolkit.SetClipboard(webcanvas.NewClipboard())
//
// Without navigator.clipboard.writeText (an insecure context) a copy stays in
// the page; paste from the browser still works, through the paste event.
func NewClipboard() *Clipboard {
	c := &Clipboard{}
	if nav := js.Global().Get("navigator").Get("clipboard"); nav.Truthy() && nav.Get("writeText").Truthy() {
		c.write = func(s string) {
			if p := nav.Call("writeText", s); p.Truthy() {
				p.Call("catch", swallow)
			}
		}
	}
	pageClipboard.Store(c)
	return c
}

// readClipboard asks the browser for its text (navigator.clipboard.readText),
// the fallback when a paste shortcut fired no paste event: Chromium's
// Ctrl+Shift+V ("paste and match style") does not on a non-editable canvas,
// and that is a terminal's paste on Linux. The browser may ask the user once.
// done runs exactly once: ok is false when the API is missing, throws, or the
// read is refused.
func readClipboard(done func(text string, ok bool)) {
	finished := false
	finish := func(text string, ok bool) {
		if !finished {
			finished = true
			done(text, ok)
		}
	}
	defer func() {
		if recover() != nil {
			finish("", false) // readText threw (an insecure context, say)
		}
	}()
	cb := js.Global().Get("navigator").Get("clipboard")
	if !cb.Truthy() || !cb.Get("readText").Truthy() {
		finish("", false)
		return
	}
	var onOK, onErr js.Func
	release := func() {
		onOK.Release()
		onErr.Release()
	}
	onOK = js.FuncOf(func(_ js.Value, args []js.Value) any {
		text := ""
		if len(args) > 0 && args[0].Type() == js.TypeString {
			text = args[0].String()
		}
		release()
		guard(func() { finish(text, true) })
		return nil
	})
	onErr = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		guard(func() { finish("", false) })
		return nil
	})
	cb.Call("readText").Call("then", onOK, onErr)
}
