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
