// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClipboardSetWritesTheBrowserAndReadsBack(t *testing.T) {
	var written []string
	c := &Clipboard{write: func(s string) { written = append(written, s) }}
	if c.ClipboardText() != "" {
		t.Fatal("a fresh clipboard is not empty")
	}
	c.SetClipboardText("ls -l")
	if c.ClipboardText() != "ls -l" || len(written) != 1 || written[0] != "ls -l" {
		t.Fatalf("text %q, written %q", c.ClipboardText(), written)
	}
	// What the browser pasted is kept and NOT written back.
	c.fromBrowser("from the OS")
	if c.ClipboardText() != "from the OS" || len(written) != 1 {
		t.Fatalf("text %q, written %q", c.ClipboardText(), written)
	}
	// The zero value works natively, with no browser.
	var z Clipboard
	z.SetClipboardText("x")
	if z.ClipboardText() != "x" {
		t.Fatal("zero-value clipboard lost its text")
	}
}

func TestBoundClipCutsOnARuneBoundary(t *testing.T) {
	if s := boundClip("short"); s != "short" {
		t.Fatalf("short text changed: %q", s)
	}
	// "é" is 2 bytes: put one across the limit.
	long := strings.Repeat("a", maxClipboard-1) + "é" + "tail"
	s := boundClip(long)
	if len(s) != maxClipboard-1 || !utf8.ValidString(s) {
		t.Fatalf("len %d valid %v", len(s), utf8.ValidString(s))
	}
	// A 4-byte rune ending exactly at the limit is kept whole.
	exact := strings.Repeat("a", maxClipboard-4) + "😀" + "more"
	if s := boundClip(exact); len(s) != maxClipboard || !strings.HasSuffix(s, "😀") {
		t.Fatalf("len %d", len(s))
	}
	// Invalid bytes do not make it eat the text: at most 3 bytes go.
	bad := strings.Repeat("\x80", maxClipboard+10)
	if s := boundClip(bad); len(s) < maxClipboard-3 {
		t.Fatalf("invalid input cut to %d", len(s))
	}
}

func TestIsPasteKey(t *testing.T) {
	for _, c := range []struct {
		key             string
		ctrl, meta, alt bool
		want            bool
	}{
		{"v", false, true, false, true}, // Cmd+V
		{"v", true, false, false, true}, // Ctrl+V
		{"V", true, false, false, true}, // Ctrl+Shift+V
		{"v", false, false, false, false},
		{"v", true, false, true, false}, // Ctrl+Alt+V
		{"c", false, true, false, false},
	} {
		if got := isPasteKey(c.key, c.ctrl, c.meta, c.alt); got != c.want {
			t.Errorf("isPasteKey(%q, ctrl %v, meta %v, alt %v) = %v", c.key, c.ctrl, c.meta, c.alt, got)
		}
	}
}

func TestPasteGateDeliversExactlyOnce(t *testing.T) {
	var g pasteGate
	c := &Clipboard{}
	// No bridge installed: nothing is held, the key goes as before.
	if g.hold(false, "v", false, true, false) {
		t.Fatal("held without a bridge")
	}
	// Not a paste key: not held.
	if g.hold(true, "c", false, true, false) {
		t.Fatal("held Cmd+C")
	}
	// Held, then the paste event: due once, with the browser's text.
	if !g.hold(true, "v", false, true, false) {
		t.Fatal("Cmd+V not held")
	}
	if !g.paste(c, "copied elsewhere") || c.ClipboardText() != "copied elsewhere" {
		t.Fatalf("paste not due, or text %q", c.ClipboardText())
	}
	if g.timeout() {
		t.Fatal("the fallback delivered a second time")
	}
	// Held, no paste event: the fallback delivers once.
	g.hold(true, "v", true, false, false)
	if !g.timeout() || g.timeout() {
		t.Fatal("fallback not exactly once")
	}
	// A paste with nothing held (the Edit menu) refreshes the text only.
	if g.paste(c, "menu") || c.ClipboardText() != "menu" {
		t.Fatalf("menu paste delivered a key, or text %q", c.ClipboardText())
	}
}
