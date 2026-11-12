// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/go-widgets/textinput"
)

func TestBoundLines(t *testing.T) {
	l, tr := boundLines("a\nb\nc", 2, 10)
	if !reflect.DeepEqual(l, []string{"a", "b"}) || !tr {
		t.Fatalf("lines %q truncated %v", l, tr)
	}
	// A line is cut to a prefix, never inside a surrogate pair.
	l, tr = boundLines("ab🙂cd", 5, 3)
	if l[0] != "ab" || !tr {
		t.Fatalf("got %q %v", l, tr)
	}
	l, tr = boundLines("ab\ncd", 5, 5)
	if len(l) != 2 || tr {
		t.Fatalf("got %q %v", l, tr)
	}
	// The real bounds hold against a hostile document.
	huge := strings.Repeat("x\n", maxLayerLines+5) + strings.Repeat("y", maxLayerLineUnits+9)
	l, tr = layerLines(huge)
	if len(l) != maxLayerLines || !tr {
		t.Fatalf("%d lines, truncated %v", len(l), tr)
	}
	l, _ = layerLines(strings.Repeat("y", maxLayerLineUnits+9))
	if textinput.Len(l[0]) != maxLayerLineUnits {
		t.Fatalf("line of %d units", textinput.Len(l[0]))
	}
}

func TestLineStart(t *testing.T) {
	if got := lineStart("ab\n🙂c\nd", 2); got != 7 {
		t.Fatalf("got %d", got)
	}
}

func TestUnitLineMapping(t *testing.T) {
	lines := []string{"ab", "", "🙂c", "é"}
	// text: "ab\n\n🙂c\né"
	cases := []struct{ off, line, unit int }{
		{0, 0, 0}, {2, 0, 2}, {3, 1, 0}, {4, 2, 0}, {6, 2, 2}, {7, 2, 3}, {8, 3, 0}, {10, 3, 2},
		{99, 3, 2}, {-5, 0, 0},
	}
	for _, c := range cases {
		l, u := unitToLine(lines, c.off)
		if l != c.line || u != c.unit {
			t.Errorf("unitToLine(%d) = %d,%d, want %d,%d", c.off, l, u, c.line, c.unit)
		}
	}
	// Round trip over every in-range offset.
	n := lineToUnit(lines, 3, 2)
	for off := 0; off <= n; off++ {
		l, u := unitToLine(lines, off)
		if got := lineToUnit(lines, l, u); got != off {
			t.Errorf("round trip %d -> %d,%d -> %d", off, l, u, got)
		}
	}
	if lineToUnit(lines, -1, -1) != 0 || lineToUnit(lines, 9, 99) != 10 || lineToUnit(lines, 0, 99) != 2 {
		t.Error("lineToUnit does not clamp")
	}
}

func TestPlanLine(t *testing.T) {
	adv := func(r rune) float64 {
		switch {
		case r < 0x80:
			return 8.4
		case r == 0x301:
			return 0
		}
		return 17
	}
	got := planLine("ab世界é", 8.5, adv)
	cell, narrow := 8.5, 8.4
	want := []span{{"ab", cell - narrow}, {"世界", cell - 17}, {"e", cell - narrow}, {"́", 8.5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if got := planLine("", 8.5, adv); got != nil {
		t.Fatalf("empty line: %v", got)
	}
	// Every rune lands on the grid: sum of advance + spacing is one cell.
	for _, s := range got {
		_ = s
	}
	for _, s := range planLine("hello world", 8.5, adv) {
		for _, r := range s.Text {
			if d := adv(r) + s.Spacing - 8.5; d > 1e-12 || d < -1e-12 {
				t.Errorf("%q is off the grid by %g", r, d)
			}
		}
	}
	if px(1.5) != "1.50000" || px(-0.1) != "-0.10000" {
		t.Errorf("px: %s %s", px(1.5), px(-0.1))
	}
}

// rects is a model on a 9x16 grid at (10, 20), pitch 18, as InputRect answers.
func rects(text string) func(int, int) (int, int, int, int) {
	return func(start, end int) (int, int, int, int) {
		l, c := textinput.LineCol(text, start)
		return 10 + 9*c, 20 + 18*l, 9 * (end - start), 16
	}
}

func TestGeomOf(t *testing.T) {
	text := "\nab\ncd"
	g, ok := geomOf(text, rects(text), 4, 6, 0.5)
	if !ok {
		t.Fatal("no geometry")
	}
	want := layerGeom{X0: 3, Y0: 7, Cell: 4.5, Pitch: 9, H: 8}
	if g != want {
		t.Fatalf("got %+v want %+v", g, want)
	}
	if g.lineTop(2) != 7+18 {
		t.Fatalf("lineTop %g", g.lineTop(2))
	}
	// A surrogate pair is one rune of two units.
	text = "🙂a"
	g, ok = geomOf(text, func(s, e int) (int, int, int, int) { return 0, 0, 9 * (e - s) / 2, 16 }, 0, 0, 1)
	if !ok || g.Cell != 9 {
		t.Fatalf("pair: %+v %v", g, ok)
	}
	// One line: the pitch is the height.
	text = "ab"
	g, _ = geomOf(text, rects(text), 0, 0, 1)
	if g.Pitch != 16 {
		t.Fatalf("pitch %g", g.Pitch)
	}
	// A second line that is not below the first leaves the pitch the height.
	g, _ = geomOf("a\nb", func(s, e int) (int, int, int, int) { return 0, 5, 9, 16 }, 0, 0, 1)
	if g.Pitch != 16 {
		t.Fatalf("pitch %g", g.Pitch)
	}
	// An empty document, or a zero cell or height, has no geometry.
	for _, text := range []string{"", "\n\n"} {
		if _, ok := geomOf(text, rects(text), 0, 0, 1); ok {
			t.Errorf("%q has geometry", text)
		}
	}
	if _, ok := geomOf("a", func(int, int) (int, int, int, int) { return 0, 0, 9, 0 }, 0, 0, 1); ok {
		t.Error("zero height has geometry")
	}
}

func TestLineStyleAndCSS(t *testing.T) {
	s := lineStyle(layerGeom{X0: 1, Y0: 2, Cell: 8, Pitch: 10, H: 9}, 3)
	for _, w := range []string{"left:1.00000px", "top:32.00000px", "height:9.00000px", "font-size:6.75000px", "white-space:pre", "position:absolute"} {
		if !strings.Contains(s, w) {
			t.Errorf("%q lacks %q", s, w)
		}
	}
	for _, w := range []string{"white-space:pre", "tab-size:1", "color:transparent", "caret-color:transparent", "::selection", "::search-text", "pointer-events:auto"} {
		if !strings.Contains(layerCSS, w) {
			t.Errorf("layerCSS lacks %q", w)
		}
	}
	h := hostStyle(1.234, 2, 300, 150)
	if !strings.Contains(h, "left:1.23px") || !strings.Contains(h, "pointer-events:none") || strings.Contains(h, "clip") {
		t.Errorf("hostStyle %q", h)
	}
}

func TestRouteKey(t *testing.T) {
	cases := []struct {
		name                  string
		key                   string
		ctrl, meta, alt, comp bool
		code                  int
		want                  keyAction
	}{
		{"letter", "a", false, false, false, false, 65, keyText},
		{"space", " ", false, false, false, false, 32, keyText},
		{"emoji", "🙂", false, false, false, false, 0, keyText},
		{"option-e on a mac", "é", false, false, true, false, 0, keyText},
		{"AltGr", "@", true, false, true, false, 0, keyText},
		{"composing", "a", false, false, false, true, 65, keyIgnore},
		{"keyCode 229", "a", false, false, false, false, 229, keyIgnore},
		{"process", "Process", false, false, false, false, 0, keyIgnore},
		{"dead", "Dead", false, false, false, false, 0, keyIgnore},
		{"unidentified", "Unidentified", false, false, false, false, 0, keyIgnore},
		{"shift", "Shift", false, false, false, false, 16, keyIgnore},
		{"control", "Control", true, false, false, false, 17, keyIgnore},
		{"alt", "Alt", false, false, true, false, 18, keyIgnore},
		{"meta", "Meta", false, true, false, false, 91, keyIgnore},
		{"altgraph", "AltGraph", false, false, false, false, 0, keyIgnore},
		{"capslock", "CapsLock", false, false, false, false, 20, keyIgnore},
		{"ctrl+c", "c", true, false, false, false, 67, keyBrowser},
		{"meta+X", "X", false, true, false, false, 88, keyBrowser},
		{"ctrl+v", "v", true, false, false, false, 86, keyBrowser},
		{"ctrl+z", "z", true, false, false, false, 90, keyScene},
		{"meta+a", "a", false, true, false, false, 65, keyScene},
		{"arrow", "ArrowLeft", false, false, false, false, 37, keyScene},
		{"enter", "Enter", false, false, false, false, 13, keyScene},
		{"backspace", "Backspace", false, false, false, false, 8, keyScene},
		{"tab", "Tab", false, false, false, false, 9, keyScene},
		{"escape", "Escape", false, false, false, false, 27, keyScene},
		{"ctrl+arrow", "ArrowLeft", true, false, false, false, 37, keyScene},
	}
	for _, c := range cases {
		if got := routeKey(c.key, c.ctrl, c.meta, c.alt, c.comp, c.code); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestRouteInput(t *testing.T) {
	for _, it := range []string{"insertText", "insertReplacementText", "insertFromPaste", "insertFromDrop", "insertFromYank", "deleteByCut", "deleteByDrag"} {
		if got := routeInput(it); got.Kind != inputReplace {
			t.Errorf("%s: %v", it, got)
		}
	}
	keys := map[string]string{"insertLineBreak": "Enter", "insertParagraph": "Enter", "deleteContentBackward": "Backspace",
		"deleteContentForward": "Delete", "historyUndo": "Ctrl+Z", "historyRedo": "Ctrl+Y"}
	for it, k := range keys {
		if got := routeInput(it); got.Kind != inputKey || got.Key != k {
			t.Errorf("%s: %v", it, got)
		}
	}
	for _, it := range []string{"insertCompositionText", "insertFromComposition", "deleteCompositionText"} {
		if got := routeInput(it); got.Kind != inputComposition {
			t.Errorf("%s: %v", it, got)
		}
	}
	if got := routeInput("deleteWordBackward"); got.Kind != inputOther {
		t.Errorf("unknown: %v", got)
	}
}

func st(text string, a, f, ms, me int) textinput.State {
	return textinput.State{Text: text, Anchor: a, Focus: f, MarkStart: ms, MarkEnd: me}
}

func TestCompositionEdit(t *testing.T) {
	// First preview: no marked range, the diff decides.
	got := compositionEdit(st("ab", 2, 2, -1, -1), st("abni", 4, 4, -1, -1))
	if got != (compEdit{Text: "ni", SelStart: 2, SelEnd: 2, ReplStart: 2, ReplEnd: 2}) {
		t.Fatalf("first: %+v", got)
	}
	// "ni" becomes "nǐ": the whole marked string stays marked.
	got = compositionEdit(st("abni", 4, 4, 2, 4), st("abnǐ", 4, 4, -1, -1))
	if got != (compEdit{Text: "nǐ", SelStart: 2, SelEnd: 2, ReplStart: 2, ReplEnd: 4}) {
		t.Fatalf("grow: %+v", got)
	}
	// It shrinks to nothing (the composition is cancelled).
	got = compositionEdit(st("abni", 4, 4, 2, 4), st("ab", 2, 2, -1, -1))
	if got.Text != "" || got.ReplStart != 2 || got.ReplEnd != 4 {
		t.Fatalf("cancel: %+v", got)
	}
	// The DOM no longer holds the marked range: the diff decides.
	got = compositionEdit(st("abni", 4, 4, 2, 4), st("a", 1, 1, -1, -1))
	if got.ReplStart != 1 || got.ReplEnd != 4 || got.Text != "" {
		t.Fatalf("lost: %+v", got)
	}
	// Selection inside the preview, clamped to it.
	got = compositionEdit(st("ab", 2, 2, -1, -1), st("abかな", 3, 99, -1, -1))
	if got.SelStart != 1 || got.SelEnd != 2 {
		t.Fatalf("selection: %+v", got)
	}
	if relSel(-4, 0, "ab") != 0 {
		t.Fatal("relSel")
	}
}

func TestLineEdit(t *testing.T) {
	old := []string{"a", "b", "c"}
	for _, c := range []struct {
		name         string
		next         []string
		at, del, ins int
	}{
		{"one line", []string{"a", "B", "c"}, 1, 1, 1},
		{"enter in b", []string{"a", "b1", "b2", "c"}, 1, 1, 2},
		{"insert a line", []string{"a", "x", "b", "c"}, 1, 0, 1},
		{"same", old, 3, 0, 0},
		{"shorter", []string{"a"}, 1, 2, 0},
		{"join b and c", []string{"a", "bc"}, 1, 2, 1},
		{"paste 5 lines", []string{"a", "b1", "p", "q", "r", "b2", "c"}, 1, 1, 5},
		{"empty to text", []string{"x"}, 0, 3, 1},
		{"text to empty", nil, 0, 3, 0},
		{"grow at the end", []string{"a", "b", "c", "d"}, 3, 0, 1},
	} {
		at, del, ins := lineEdit(old, c.next)
		if at != c.at || del != c.del || ins != c.ins {
			t.Errorf("%s: got (%d,%d,%d), want (%d,%d,%d)", c.name, at, del, ins, c.at, c.del, c.ins)
		}
	}
	// Applying the splice to old gives next, on a few random edits.
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 500; i++ {
		a := make([]string, r.IntN(6))
		for j := range a {
			a[j] = string(rune('a' + r.IntN(3)))
		}
		b := make([]string, r.IntN(6))
		for j := range b {
			b[j] = string(rune('a' + r.IntN(3)))
		}
		at, del, ins := lineEdit(a, b)
		got := append(append(append([]string{}, a[:at]...), b[at:at+ins]...), a[at+del:]...)
		if !reflect.DeepEqual(got, append([]string{}, b...)) && (len(got) != 0 || len(b) != 0) {
			t.Fatalf("splice of %v to %v gave %v", a, b, got)
		}
	}
}

func TestFlowLayout(t *testing.T) {
	if !(layerGeom{Pitch: 18, H: 18}).flowOK() || !(layerGeom{Pitch: 9.5, H: 9}).flowOK() {
		t.Error("a pitch on the 1/64 grid must flow")
	}
	if (layerGeom{Pitch: 37 / 3.0, H: 12}).flowOK() || (layerGeom{Pitch: 12, H: 0.3}).flowOK() {
		t.Error("a pitch off the grid must be placed line by line")
	}
	g := layerGeom{X0: 1, Y0: 2, Cell: 8, Pitch: 10, H: 9}
	first, rest := flowStyle(g, true), flowStyle(g, false)
	for _, w := range []string{"display:block", "height:10.00000px", "line-height:9.00000px", "font-size:6.75000px", "white-space:pre", "margin:2.00000px 0 0 1.00000px"} {
		if !strings.Contains(first, w) {
			t.Errorf("first %q lacks %q", first, w)
		}
	}
	if !strings.Contains(rest, "margin:0 0 0 1.00000px") || strings.Contains(rest, "position:absolute") {
		t.Errorf("rest %q", rest)
	}
}
