// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for the text layer: one canvas holding one text widget, a
// plain model on the toolkit's grid (one rune is one cell) that implements
// textinput.Client. The scene counts every Char and KeyDown it hears, so a test
// can prove that typing reaches it once and only through the layer.
package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"syscall/js"
	"time"

	"github.com/go-widgets/textinput"
	"github.com/go-widgets/webcanvas"
)

// The grid, in CSS pixels at ratio 1; every surface length is this times dpr.
const (
	cellW  = 9
	lineH  = 16
	pitchH = 18
	padX   = 10
	padY   = 12
)

type model struct {
	text         string
	anchor, foc  int
	markS, markE int
	rev          uint64
	dpr          float64
	readOnly     bool
}

func (m *model) InputText() string          { return m.text }
func (m *model) InputRevision() uint64      { return m.rev }
func (m *model) InputSelection() (int, int) { return m.anchor, m.foc }
func (m *model) InputMarked() (int, int)    { return m.markS, m.markE }
func (m *model) InputMultiline() bool       { return true }
func (m *model) InputReadOnly() bool        { return m.readOnly }
func (m *model) InputIndexAt(x, y int) int  { return 0 }
func (m *model) SetInputSelection(a, f int) { m.anchor, m.foc = a, f }
func (m *model) set(text string, caret int) {
	m.text, m.anchor, m.foc, m.markS, m.markE = text, caret, caret, -1, -1
	m.rev++
}
func (m *model) replace(s, e int, t string) {
	m.text = textinput.Edit{Start: s, End: e, Text: t}.Apply(m.text)
}

func (m *model) InputRect(start, end int) (x, y, w, h int) {
	l, c := textinput.LineCol(m.text, start)
	runes := textinput.ToRune(m.text, end) - textinput.ToRune(m.text, start)
	d := m.dpr
	return int(float64(padX+cellW*c) * d), int(float64(padY+pitchH*l) * d), int(float64(cellW*runes) * d), int(float64(lineH) * d)
}

func (m *model) target(s, e int) (int, int) {
	if s >= 0 {
		return s, e
	}
	if m.markS >= 0 {
		return m.markS, m.markE
	}
	return min(m.anchor, m.foc), max(m.anchor, m.foc)
}

// stamp tells the keystroke benchmark (the page's __stamp, when it installed
// one) that the model or the canvas was updated, and when.
func stamp(kind string) {
	if f := js.Global().Get("__stamp"); f.Type() == js.TypeFunction {
		f.Invoke(kind)
	}
}

func (m *model) ReplaceInput(s, e int, t string) {
	s, e = m.target(s, e)
	m.replace(s, e, t)
	m.set(m.text, s+textinput.Len(t))
	stamp("model")
}

func (m *model) SetMarkedInput(t string, ss, se, rs, re int) {
	s, e := m.target(rs, re)
	m.replace(s, e, t)
	m.markS, m.markE = s, s+textinput.Len(t)
	m.anchor, m.foc = s+ss, s+se
	m.rev++
	stamp("model")
}

func (m *model) UnmarkInput() {
	if m.markS >= 0 {
		m.markS, m.markE = -1, -1
		m.rev++
		stamp("model")
	}
}

type scene struct {
	m       *model
	dpr     float64
	focused bool
	consume bool
	chars   int
	keys    []string
}

func (s *scene) Size() (int, int) { return int(400 * s.dpr), int(300 * s.dpr) }
func (s *scene) Draw([]byte)      { stamp("draw") }
func (s *scene) Click(int, int) bool {
	return false
}
func (s *scene) Release(int, int) bool { return false }
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(string) bool      { s.chars++; return true }
func (s *scene) KeyDown(k string) bool {
	s.keys = append(s.keys, k)
	defer stamp("model")
	m := s.m
	lo, hi := min(m.anchor, m.foc), max(m.anchor, m.foc)
	switch k {
	case "Backspace":
		if lo == hi && lo > 0 {
			lo--
		}
		m.replace(lo, hi, "")
		m.set(m.text, lo)
		return true
	case "Enter":
		m.replace(lo, hi, "\n")
		m.set(m.text, lo+1)
		return true
	case "ArrowLeft":
		m.anchor, m.foc = max(m.foc-1, 0), max(m.foc-1, 0)
		return true
	case "ArrowRight":
		n := min(m.foc+1, textinput.Len(m.text))
		m.anchor, m.foc = n, n
		return true
	}
	return false
}

// Scroll is a webcanvas.ScrollConsumer: it records every wheel it is given in
// window.__wheels as [dx, dy], and uses the wheel (the page must not scroll)
// only after setConsume(true).
func (s *scene) Scroll(x, y, dx, dy int) (repaint, consumed bool) {
	w := js.Global().Get("__wheels")
	if w.Type() != js.TypeObject {
		w = js.Global().Get("Array").New()
		js.Global().Set("__wheels", w)
	}
	w.Call("push", []any{dx, dy})
	return false, s.consume
}

func (s *scene) A11yNodes() []webcanvas.A11yNode {
	w, h := s.Size()
	return []webcanvas.A11yNode{{Key: "code", Role: "textbox", Name: "Go cell 1", Value: s.m.text,
		X: 0, Y: 0, W: w, H: h, Focused: s.focused, Multiline: true}}
}

func (s *scene) TextInputFor(key string) textinput.Client {
	if key == "code" {
		return s.m
	}
	return nil
}

func main() {
	g := js.Global()
	dpr := g.Get("devicePixelRatio").Float()
	m := &model{dpr: dpr, markS: -1, markE: -1}
	sc := &scene{m: m, dpr: dpr}
	// A page opened as #text starts with that text, so the textbox is created
	// with a value already in it (the usual case of a real scene).
	if h := g.Get("location").Get("hash").String(); len(h) > 1 {
		m.set(h[1:], 0)
	}
	surf := webcanvas.Mount(g.Get("document").Call("getElementById", "screen"), sc)
	g.Set("setText", js.FuncOf(func(_ js.Value, a []js.Value) any {
		m.set(a[0].String(), 0)
		surf.Refresh()
		return nil
	}))
	g.Set("setFocus", js.FuncOf(func(_ js.Value, a []js.Value) any {
		sc.focused = a[0].Bool()
		if sc.focused {
			surf.Focus()
		}
		surf.Refresh()
		return nil
	}))
	g.Set("setConsume", js.FuncOf(func(_ js.Value, a []js.Value) any {
		sc.consume = a[0].Bool()
		return nil
	}))
	g.Set("setSelection", js.FuncOf(func(_ js.Value, a []js.Value) any {
		m.anchor, m.foc = a[0].Int(), a[1].Int()
		surf.Refresh()
		return nil
	}))
	g.Set("modelState", js.FuncOf(func(js.Value, []js.Value) any {
		b, _ := json.Marshal(map[string]any{"text": m.text, "anchor": m.anchor, "focus": m.foc,
			"markS": m.markS, "markE": m.markE, "chars": sc.chars, "keys": sc.keys})
		return string(b)
	}))
	// bench(lines, rounds): the milliseconds of one keystroke, median over the
	// rounds: the model takes an "a" at the caret, the layer rewrites its line
	// and the browser lays the result out. Rounds are interleaved across the
	// lines of the document so no round repeats the one before.
	g.Set("bench", js.FuncOf(func(_ js.Value, a []js.Value) any {
		n, rounds := a[0].Int(), a[1].Int()
		text := ""
		for i := 0; i < n; i++ {
			if i > 0 {
				text += "\n"
			}
			text += "    result := compute(" + strconv.Itoa(i) + ") // line " + strconv.Itoa(i)
		}
		t0 := time.Now()
		m.set(text, 0)
		surf.Refresh()
		g.Get("document").Get("body").Get("offsetHeight")
		build := float64(time.Since(t0).Microseconds()) / 1000
		var ms []float64
		for r := 0; r < rounds; r++ {
			line := (r * 7919) % n
			off := textinput.Offset(m.text, line, 4)
			m.anchor, m.foc = off, off
			t := g.Get("performance").Call("now").Float()
			m.ReplaceInput(-1, -1, "a")
			surf.Refresh()
			g.Get("document").Get("body").Get("offsetHeight") // force layout
			ms = append(ms, g.Get("performance").Call("now").Float()-t)
		}
		slices.Sort(ms)
		return []any{build, ms[len(ms)/2], ms[0], ms[len(ms)-1]}
	}))
	g.Set("textReady", true)
	select {}
}
