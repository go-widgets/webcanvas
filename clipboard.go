// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"sync"
	"unicode/utf8"
)

// This file is the pure half of the browser clipboard bridge; clipboard_js.go
// is the glue. A canvas has no clipboard of its own: a toolkit that keeps one
// in memory (go-widgets/toolkit's default, until a host calls SetClipboard)
// copies and pastes only within the page. The bridge connects that clipboard
// to the browser's:
//
//   - Copy: SetClipboardText also hands the text to navigator.clipboard
//     .writeText, which the Clipboard API allows during a user activation; a
//     copy runs inside the keydown that asked for it.
//   - Paste: the only synchronous, prompt-free read is a paste event's
//     clipboardData (Clipboard API and events, "fire a clipboard event": the
//     target is the focused node when nothing is editable). So a paste
//     shortcut is held at keydown and not prevented; the browser then fires
//     paste, the bridge takes its text, and only then is the key delivered:
//     the widget's own paste reads the text the user copied. If no paste event
//     comes, the key is delivered anyway, as it was before the bridge.
//
// The key the scene sees is unchanged, only its timing: a terminal that sends
// Ctrl+V as ^V still does.

// maxClipboard bounds the text the bridge keeps, in bytes: a paste is the
// user's, but the scene must not be handed an unbounded string.
const maxClipboard = 1 << 20

// Clipboard is a text clipboard backed by the browser's. It has the method set
// of go-widgets/toolkit's Clipboard, so a host installs it with
// toolkit.SetClipboard(webcanvas.NewClipboard()). The zero value is an
// in-memory clipboard; NewClipboard (js/wasm) connects it to the browser and
// makes it the one paste shortcuts wait for.
type Clipboard struct {
	mu    sync.Mutex
	text  string
	write func(string) // the browser's writeText, nil natively
}

// ClipboardText returns the current text: the last paste the browser
// delivered, or the last text set.
func (c *Clipboard) ClipboardText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

// SetClipboardText replaces the text and writes it to the browser's clipboard.
func (c *Clipboard) SetClipboardText(s string) {
	s = boundClip(s)
	c.mu.Lock()
	c.text = s
	w := c.write
	c.mu.Unlock()
	if w != nil {
		w(s)
	}
}

// fromBrowser takes the text a paste event carried; it is not written back.
func (c *Clipboard) fromBrowser(s string) {
	s = boundClip(s)
	c.mu.Lock()
	c.text = s
	c.mu.Unlock()
}

// boundClip cuts s to maxClipboard bytes on a rune boundary.
func boundClip(s string) string {
	if len(s) <= maxClipboard {
		return s
	}
	// s[i] is the byte just past the kept text: back up until it starts a rune,
	// at most utf8.UTFMax-1 steps for valid UTF-8.
	i := maxClipboard
	for k := 0; k < utf8.UTFMax-1 && !utf8.RuneStart(s[i]); k++ {
		i--
	}
	return s[:i]
}

// isPasteKey reports a paste shortcut: V with Ctrl or Meta, not Alt (Shift
// allowed: Ctrl+Shift+V is a terminal's paste).
func isPasteKey(key string, ctrl, meta, alt bool) bool {
	return (key == "v" || key == "V") && (ctrl || meta) && !alt
}

// pasteGate holds a paste shortcut until the browser's paste event or a
// fallback, and delivers it exactly once.
type pasteGate struct{ pending bool }

// hold reports whether the keydown must be held (not delivered, not
// prevented). It holds only when a bridge is installed.
func (g *pasteGate) hold(installed bool, key string, ctrl, meta, alt bool) bool {
	if !installed || !isPasteKey(key, ctrl, meta, alt) {
		return false
	}
	g.pending = true
	return true
}

// paste records the browser's text and reports whether a held key is now due.
// A paste with no held key (the Edit menu) only refreshes the text.
func (g *pasteGate) paste(c *Clipboard, text string) bool {
	c.fromBrowser(text)
	due := g.pending
	g.pending = false
	return due
}

// timeout reports whether the held key is still due when no paste event came.
func (g *pasteGate) timeout() bool {
	due := g.pending
	g.pending = false
	return due
}
