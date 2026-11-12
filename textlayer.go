// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-widgets/textinput"
)

// This file is the pure half of the text layer (#21, revision 1, Q4): a DOM
// layer over a canvas text widget that is at once the widget's accessibility
// element, its editing host and the text the browser's find and selection see.
// textlayer_js.go is the glue. Nothing here touches syscall/js, so all of it is
// tested natively at 100%.
//
// Sources opened for it:
//   - HTML Standard, contenteditable: the keywords are true, false and
//     plaintext-only, and "an editing host is either an HTML element with its
//     contenteditable attribute in the true state or plaintext-only state".
//   - Input Events Level 2, inputType table: insertText, insertLineBreak,
//     insertParagraph, deleteContentBackward/Forward, historyUndo and
//     insertFromPaste are cancelable; insertCompositionText is not.
//   - UI Events: composition runs start, update*, end; keydown and keyup have
//     isComposing true between compositionstart and compositionend.
//   - PDF.js text_layer.js and text_layer_builder.css: transparent text,
//     white-space: pre, a cap on the number of text divs.

// TextInputHost is implemented by a scene that has text widgets. The mirror asks
// it, for every node whose role is textbox, for the widget's text-input client;
// a nil answer leaves that node a plain <div role=textbox>.
type TextInputHost interface {
	// TextInputFor returns the client of the text widget published under key
	// (the node's Key), or nil.
	TextInputFor(key string) textinput.Client
}

// Bounds on what an untrusted notebook can make the layer build.
const (
	// maxLayers is the number of text layers one surface holds.
	maxLayers = 32
	// maxLayerLines and maxLayerLineUnits bound one layer: the lines kept, and
	// the UTF-16 units kept of each line. Text past them is not in the DOM.
	maxLayerLines     = 20000
	maxLayerLineUnits = 4000
)

// layerLines splits text into the lines the layer shows, bounded by
// maxLayerLines and maxLayerLineUnits. A shown line is a prefix of the model's
// line, so every offset inside it maps one to one. truncated says that
// something was dropped.
func layerLines(text string) (lines []string, truncated bool) {
	return boundLines(text, maxLayerLines, maxLayerLineUnits)
}

func boundLines(text string, maxLines, maxUnits int) ([]string, bool) {
	lines := strings.Split(text, "\n")
	truncated := false
	if len(lines) > maxLines {
		lines, truncated = lines[:maxLines], true
	}
	for i, l := range lines {
		if textinput.Len(l) > maxUnits {
			lines[i], truncated = l[:textinput.ToByte(l, maxUnits)], true
		}
	}
	return lines, truncated
}

// lineStart is the model offset (UTF-16) of the start of line i of text.
func lineStart(text string, line int) int { return textinput.Offset(text, line, 0) }

// unitToLine maps a model offset onto (line, UTF-16 units into that line) over
// the given lines (joined by one "\n" each). An offset past the end is clamped
// to the end of the last line; a negative one to 0.
func unitToLine(lines []string, off int) (line, unit int) {
	if off < 0 {
		off = 0
	}
	for i, l := range lines {
		n := textinput.Len(l)
		if off <= n {
			return i, off
		}
		off -= n + 1
	}
	last := len(lines) - 1
	return last, textinput.Len(lines[last])
}

// lineToUnit is the inverse of [unitToLine]: the model offset of (line, unit).
// A line outside the lines, or a unit past the end of its line, is clamped.
func lineToUnit(lines []string, line, unit int) int {
	line = min(max(line, 0), len(lines)-1)
	off := 0
	for i := 0; i < line; i++ {
		off += textinput.Len(lines[i]) + 1
	}
	return off + min(max(unit, 0), textinput.Len(lines[line]))
}

// span is a run of runes that share one letter-spacing.
type span struct {
	Text    string
	Spacing float64 // CSS px added after each rune of the run
}

// planLine puts every rune of line on the toolkit's grid, where one rune is one
// cell of width cell (CSS px). adv is the width table: the natural advance of a
// rune in the layer's font. A rune whose advance is not the cell gets a
// letter-spacing that makes up the difference (negative when the glyph is
// wider), so rune i starts at exactly i*cell. Runs of runes with the same
// spacing share a span. Plain inline spans, no transforms and no inline-blocks:
// find and selection run across them as across any text (revision 1, Q4).
func planLine(line string, cell float64, adv func(rune) float64) []span {
	var out []span
	start := 0
	cur := math.NaN()
	for i, r := range line {
		sp := cell - adv(r)
		if sp != cur {
			if i > start {
				out = append(out, span{line[start:i], cur})
			}
			start, cur = i, sp
		}
	}
	if len(line) > start {
		out = append(out, span{line[start:], cur})
	}
	return out
}

// px writes a length for a style attribute: five decimals, trimmed. At 5e-6 px
// per rune the error stays far under 0.05 px over a line of the longest size.
func px(v float64) string {
	return strconv.FormatFloat(v, 'f', 5, 64)
}

// layerGeom is where the model puts its text, in CSS pixels relative to the
// layer's element.
type layerGeom struct {
	X0, Y0 float64 // top-left of line 0 (the horizontal scroll is in X0)
	Cell   float64 // the width of one rune
	Pitch  float64 // the distance between two lines
	H      float64 // the height of a line
}

// geomOf reads the layout from the client's InputRect, in surface pixels,
// relative to the node at (nodeX, nodeY), scaled to CSS pixels by scale. ok is
// false when the model gives no usable cell (an empty document): the layer then
// has nothing to place.
func geomOf(text string, rect func(start, end int) (x, y, w, h int), nodeX, nodeY int, scale float64) (g layerGeom, ok bool) {
	x0, y0, _, h0 := rect(0, 0)
	g = layerGeom{X0: float64(x0-nodeX) * scale, Y0: float64(y0-nodeY) * scale, H: float64(h0) * scale}
	g.Pitch = g.H
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		_, y1, _, _ := rect(textinput.Len(lines[0])+1, textinput.Len(lines[0])+1)
		if d := float64(y1-y0) * scale; d > 0 {
			g.Pitch = d
		}
	}
	// The cell is the width of the first rune of the first non-empty line.
	off := 0
	for _, l := range lines {
		if l != "" {
			r, _ := utf8.DecodeRuneInString(l)
			n := 1
			if r >= 0x10000 {
				n = 2
			}
			_, _, w, _ := rect(off, off+n)
			g.Cell = float64(w) * scale
			break
		}
		off++
	}
	if g.Cell <= 0 || g.H <= 0 || !finite(g.Cell) || !finite(g.Pitch) || !finite(g.X0) || !finite(g.Y0) {
		return layerGeom{}, false
	}
	return g, true
}

// lineTop is the top of line i, in CSS pixels.
func (g layerGeom) lineTop(i int) float64 { return g.Y0 + float64(i)*g.Pitch }

// FontSize is the layer's font size in CSS px. The glyphs are never seen, so it
// only has to be the size at which the width table is measured.
func (g layerGeom) FontSize() float64 { return g.H * 0.75 }

// lineStyle is the style of the div of line i: placed at its model position,
// one line high, text kept as written.
func lineStyle(g layerGeom, i int) string {
	return "position:absolute;margin:0;padding:0;border:0;left:" + px(g.X0) + "px;top:" + px(g.lineTop(i)) +
		"px;height:" + px(g.H) + "px;line-height:" + px(g.H) + "px;font-size:" + px(g.FontSize()) + "px;white-space:pre"
}

// layerCSS is the one stylesheet of the text layer. Text and caret are
// transparent: the canvas still paints the glyphs, colours and caret. The
// browser paints the selection and the find matches, with the glyphs showing
// through (CSS Pseudo-Elements 4, ::selection and ::search-text, which a
// browser may not implement: then its default find colours apply). Each
// pseudo-element has a rule of its own, because a selector the browser does not
// know drops the whole rule it is in.
const layerCSS = `[data-webcanvas-layer]{position:absolute;margin:0;padding:0;border:0;overflow:hidden;` +
	`pointer-events:auto;outline:none;white-space:pre;tab-size:1;-moz-tab-size:1;` +
	`font-family:monospace;color:transparent;caret-color:transparent;-webkit-text-fill-color:transparent;` +
	`user-select:text;-webkit-user-select:text}` +
	`[data-webcanvas-layer]::selection{color:transparent;-webkit-text-fill-color:transparent;background:color-mix(in srgb,Highlight 45%,transparent)}` +
	`[data-webcanvas-layer] ::selection{color:transparent;-webkit-text-fill-color:transparent;background:color-mix(in srgb,Highlight 45%,transparent)}` +
	`[data-webcanvas-layer] ::search-text{color:transparent;background:color-mix(in srgb,Mark 45%,transparent)}` +
	`[data-webcanvas-layer] ::search-text:current{color:transparent;background:color-mix(in srgb,Mark 80%,transparent)}`

// hostStyle is the style of the container of the layers: the canvas' box,
// visible (the clipped mirror root would hide the layers), and transparent to
// the pointer except where a layer is.
func hostStyle(left, top, w, h float64) string {
	f := func(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) + "px" }
	return "position:absolute;margin:0;padding:0;border:0;overflow:hidden;pointer-events:none;left:" +
		f(left) + ";top:" + f(top) + ";width:" + f(w) + ";height:" + f(h)
}

// keyAction is what a surface does with a keydown on a text layer.
type keyAction int

const (
	// keyIgnore: an input method owns the key, or it is a bare modifier.
	keyIgnore keyAction = iota
	// keyScene: deliver App.KeyDown; prevent the default only if the scene
	// consumed the key, then bring the DOM selection to the model.
	keyScene
	// keyBrowser: Ctrl/Meta+C, X or V. Not prevented: the browser fires copy,
	// cut and paste on the layer.
	keyBrowser
	// keyText: a printable key. Not delivered at keydown: the text arrives
	// through beforeinput, which is what keeps it from being inserted twice.
	keyText
)

// routeKey classifies one keydown (revision 1, "Input"; the first-draft design's
// route). composing is KeyboardEvent.isComposing, keyCode the legacy code, which
// is 229 when an input method processes the key (UI Events).
func routeKey(key string, ctrl, meta, alt, composing bool, keyCode int) keyAction {
	switch {
	case composing || keyCode == 229 || key == "Process" || key == "Dead" || key == "Unidentified":
		return keyIgnore
	case key == "Shift" || key == "Control" || key == "Alt" || key == "Meta" || key == "AltGraph" || key == "CapsLock":
		return keyIgnore
	}
	single := utf8.RuneCountInString(key) == 1
	if single && (ctrl || meta) && !(ctrl && alt && !meta) { // AltGr is Ctrl+Alt: text
		switch strings.ToLower(key) {
		case "c", "x", "v":
			return keyBrowser
		}
		return keyScene
	}
	if single {
		return keyText
	}
	return keyScene
}

// inputKind is what a surface does with one beforeinput.
type inputKind int

const (
	// inputOther: not understood. Left to the browser; the DOM is reconciled
	// with the model afterwards.
	inputOther inputKind = iota
	// inputReplace: prevent, then replace the selection with the event's text
	// (empty for a cut).
	inputReplace
	// inputKey: prevent, then deliver Key as App.KeyDown, so auto-indent, undo
	// grouping and the rest stay on the one code path.
	inputKey
	// inputComposition: not cancelable (Input Events 2); the DOM changes in
	// place and the composition handlers read it.
	inputComposition
)

type inputAction struct {
	Kind inputKind
	Key  string
}

// routeInput maps an InputEvent.inputType (Input Events Level 2) onto an action.
// Mobile keyboards send these instead of keydowns.
func routeInput(inputType string) inputAction {
	switch inputType {
	case "insertText", "insertReplacementText", "insertFromPaste", "insertFromDrop", "insertFromYank", "deleteByCut", "deleteByDrag":
		return inputAction{Kind: inputReplace}
	case "insertLineBreak", "insertParagraph":
		return inputAction{Kind: inputKey, Key: "Enter"}
	case "deleteContentBackward":
		return inputAction{Kind: inputKey, Key: "Backspace"}
	case "deleteContentForward":
		return inputAction{Kind: inputKey, Key: "Delete"}
	case "historyUndo":
		return inputAction{Kind: inputKey, Key: "Ctrl+Z"}
	case "historyRedo":
		return inputAction{Kind: inputKey, Key: "Ctrl+Y"}
	case "insertCompositionText", "insertFromComposition", "deleteCompositionText":
		return inputAction{Kind: inputComposition}
	}
	return inputAction{}
}

// compEdit is one SetMarkedInput call.
type compEdit struct {
	Text               string
	SelStart, SelEnd   int // the selection inside Text
	ReplStart, ReplEnd int // the model range Text replaces
}

// compositionEdit turns what the DOM shows during a composition (next) into the
// call that brings the model (prev, which holds the previous preview) to it.
// With a marked range in prev, the composition string is that range grown or
// shrunk by the change in length, so a pinyin "ni" that becomes "nǐ" keeps its
// whole string marked and not just the letter that changed. Without one, the
// range is what [textinput.Reconcile] deduces. If the DOM no longer holds the
// marked range (the user clicked away), the same fallback applies.
func compositionEdit(prev, next textinput.State) compEdit {
	ms, me := prev.MarkStart, prev.MarkEnd
	if ms >= 0 && me >= ms {
		end := me + textinput.Len(next.Text) - textinput.Len(prev.Text)
		if end >= ms && end <= textinput.Len(next.Text) {
			t := next.Text[textinput.ToByte(next.Text, ms):textinput.ToByte(next.Text, end)]
			return compEdit{Text: t, SelStart: relSel(next.Anchor, ms, t), SelEnd: relSel(next.Focus, ms, t), ReplStart: ms, ReplEnd: me}
		}
	}
	e := textinput.Reconcile(prev, next)
	return compEdit{Text: e.Text, SelStart: relSel(next.Anchor, e.Start, e.Text), SelEnd: relSel(next.Focus, e.Start, e.Text),
		ReplStart: e.Start, ReplEnd: e.End}
}

// relSel is the selection offset off made relative to a text that starts at
// start, clamped into the text.
func relSel(off, start int, text string) int {
	return min(max(off-start, 0), textinput.Len(text))
}

// lineEdit is the one splice that turns old into next: the del lines of old
// from index at are replaced by the ins lines of next from the same index, and
// every line before at and after the splice is the same. It is the longest
// common prefix and then the longest common suffix, so typing in a line is a
// splice of 1 for 1, Enter and a paste of lines are 1 for 1+k, Backspace at a
// line start is 2 for 1, and the lines after the splice are not touched: their
// elements only move, which in the flow layout costs nothing.
func lineEdit(old, next []string) (at, del, ins int) {
	n := min(len(old), len(next))
	for at < n && old[at] == next[at] {
		at++
	}
	s := 0
	for s < n-at && old[len(old)-1-s] == next[len(next)-1-s] {
		s++
	}
	return at, len(old) - at - s, len(next) - at - s
}

// flowOK says that the lines may be laid out in the normal flow, one block
// after the other, so that inserting or removing a line moves the rest without
// touching it. A browser keeps lengths on a grid of 1/64 px (Blink's
// LayoutUnit): a pitch off that grid would be rounded the same way on every
// line, and the error would add up down the document. Then the lines are
// placed one by one instead, each at its own top.
func (g layerGeom) flowOK() bool {
	p := g.Pitch * 64
	return math.Abs(p-math.Round(p)) < 1e-6 && math.Abs(g.H*64-math.Round(g.H*64)) < 1e-6 && g.Pitch < 1<<20
}

// flowStyle is the style of a line div in the flow layout: a block of the
// pitch's height, the text one line high at its top. The first line carries the
// offset of the text's top (the layer is a block formatting context, so the
// margin does not collapse through it).
func flowStyle(g layerGeom, first bool) string {
	top := "0"
	if first {
		top = px(g.Y0) + "px"
	}
	return "display:block;margin:" + top + " 0 0 " + px(g.X0) + "px;padding:0;border:0;height:" + px(g.Pitch) +
		"px;line-height:" + px(g.H) + "px;font-size:" + px(g.FontSize()) + "px;white-space:pre"
}
