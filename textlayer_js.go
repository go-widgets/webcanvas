// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package webcanvas

import (
	"slices"
	"strings"
	"syscall/js"

	"github.com/go-widgets/textinput"
)

// layer is the DOM text layer of one text widget (#21, revision 1, Q4): the
// element the mirror emits for a textbox node. Its decisions are in textlayer.go.
//
// Untrusted text: the code comes from the app. It reaches the DOM only through
// textContent; there is no innerHTML, outerHTML or insertAdjacentHTML.
type layer struct {
	m   *mirror
	key string
	el  js.Value
	c   textinput.Client
	led listeners

	node   A11yNode
	scale  float64
	text   string
	lines  []string
	cut    bool // lines were bounded: the DOM does not hold the whole text
	rev    uint64
	hasRev bool
	geom   layerGeom
	hasG   bool
	divs   []js.Value
	flow   bool     // the lines are blocks in the flow, not placed one by one
	first  js.Value // the div styled as the first line (flow layout)
	drawn  []string // the text of each div as the layer wrote it, so a keystroke reads no DOM
	fs     float64
	adv    map[rune]float64
	ctx    js.Value

	composing bool
	dirty     bool // a beforeinput the layer did not understand: reconcile at input
	lastSel   [2]int
	hasSel    bool
}

var layerCSSOnce bool

// ensureLayerCSS puts the layer stylesheet in the page, once.
func ensureLayerCSS() {
	if layerCSSOnce {
		return
	}
	layerCSSOnce = true
	d := doc()
	st := d.Call("createElement", "style")
	st.Set("textContent", layerCSS)
	d.Get("head").Call("appendChild", st)
}

func (m *mirror) newLayer(el js.Value, key string, c textinput.Client) *layer {
	ensureLayerCSS()
	l := &layer{m: m, key: key, el: el, c: c, adv: map[rune]float64{}}
	// The mirror made el with the node's value as its text. The layer's text is
	// its lines, which sync writes; left in place, the first text would sit
	// beside them and the browser would read the value twice.
	el.Set("textContent", "")
	el.Call("setAttribute", "data-webcanvas-layer", "")
	el.Call("setAttribute", "spellcheck", "false")
	el.Call("setAttribute", "autocapitalize", "off")
	el.Call("setAttribute", "autocorrect", "off")
	l.ctx = doc().Call("createElement", "canvas").Call("getContext", "2d")
	on(&l.led, el, "focusin", nil, func(js.Value) {
		// Editable only while focused: unfocused, the layer is plain text, so a
		// selection dragged between prose and code is an ordinary one.
		el.Call("setAttribute", "contenteditable", "plaintext-only")
		l.writeSelection(true)
	})
	on(&l.led, el, "focusout", nil, func(js.Value) {
		el.Call("removeAttribute", "contenteditable")
	})
	on(&l.led, el, "keydown", nil, l.keydown)
	on(&l.led, el, "beforeinput", nil, l.beforeinput)
	on(&l.led, el, "compositionstart", nil, func(js.Value) { l.composing = true })
	on(&l.led, el, "input", nil, l.input)
	on(&l.led, el, "compositionend", nil, l.compositionend)
	on(&l.led, doc(), "selectionchange", nil, l.selectionchange)
	m.s.all.add(func() {}, l.led.close)
	return l
}

func (l *layer) close() {
	l.led.close()
	l.el.Call("remove")
}

// width is the natural advance of r in the layer's font, measured once.
func (l *layer) width(r rune) float64 {
	if w, ok := l.adv[r]; ok {
		return w
	}
	w := l.ctx.Call("measureText", string(r)).Get("width").Float()
	l.adv[r] = w
	return w
}

// sync brings the lines to the model. It is cheap when nothing changed: one
// revision read and the geometry. It does nothing while a composition is in
// flight, because the browser owns the DOM until compositionend (the Flutter
// rule of the design).
func (l *layer) sync(n A11yNode, scale float64) {
	l.node, l.scale = n, scale
	if l.composing {
		return
	}
	changed := false
	if rev := l.c.InputRevision(); !l.hasRev || rev != l.rev {
		l.text = l.c.InputText()
		l.lines, l.cut = layerLines(l.text)
		l.rev, l.hasRev, changed = rev, true, true
	}
	g, ok := geomOf(l.text, l.c.InputRect, n.X, n.Y, scale)
	if !ok {
		l.truncateTo(0)
		l.hasG = false
		return
	}
	all := !l.hasG || g != l.geom
	if all || changed {
		if fs := g.FontSize(); fs != l.fs {
			l.fs = fs
			l.adv = map[rune]float64{}
			l.ctx.Set("font", px(fs)+"px monospace")
		}
		if all {
			l.geom, l.hasG = g, true
			l.flow = g.flowOK()
			l.renderAll()
		} else {
			l.splice(lineEdit(l.drawn, l.lines))
		}
	}
	l.writeSelection(false)
}

func (l *layer) truncateTo(n int) {
	for len(l.divs) > n {
		l.divs[len(l.divs)-1].Call("remove")
		l.divs = l.divs[:len(l.divs)-1]
		l.drawn = l.drawn[:len(l.drawn)-1]
	}
}

// style writes the style line i has now. In the flow layout all lines but the
// first share one style; otherwise each line has its own top.
func (l *layer) style(i int) {
	s := ""
	switch {
	case !l.flow:
		s = lineStyle(l.geom, i)
	case i == 0:
		s = flowStyle(l.geom, true)
		l.first = l.divs[0]
	default:
		s = flowStyle(l.geom, false)
	}
	l.divs[i].Call("setAttribute", "style", s)
}

// writeLine writes the text of line i into its div, as spans on the grid.
func (l *layer) writeLine(i int) {
	d := l.divs[i]
	l.drawn[i] = l.lines[i]
	d.Set("textContent", "")
	for _, sp := range planLine(l.lines[i], l.geom.Cell, l.width) {
		s := doc().Call("createElement", "span")
		s.Get("style").Set("letterSpacing", px(sp.Spacing)+"px")
		s.Set("textContent", sp.Text)
		d.Call("appendChild", s)
	}
}

// renderAll writes every line, restyled: the geometry changed, or the layer
// was reset.
func (l *layer) renderAll() {
	l.truncateTo(len(l.lines))
	l.first = js.Value{}
	for i := range l.lines {
		if i >= len(l.divs) {
			d := doc().Call("createElement", "div")
			l.el.Call("appendChild", d)
			l.divs = append(l.divs, d)
			l.drawn = append(l.drawn, "")
		}
		l.style(i)
		l.writeLine(i)
	}
}

// splice applies a [lineEdit]: the lines of the splice are rewritten, inserted
// or removed, and no other line element is touched in the flow layout. Where
// the lines are placed one by one, the lines after the splice get their new
// top, a style and nothing else.
func (l *layer) splice(at, del, ins int) {
	prev := l.first // the div that carries the text's top offset, before the splice
	for k := 0; k < min(del, ins); k++ {
		l.writeLine(at + k)
	}
	for k := del; k < ins; k++ {
		i := at + k
		d := doc().Call("createElement", "div")
		if i < len(l.divs) {
			l.el.Call("insertBefore", d, l.divs[i])
		} else {
			l.el.Call("appendChild", d)
		}
		l.divs = slices.Insert(l.divs, i, d)
		l.drawn = slices.Insert(l.drawn, i, "")
		l.style(i)
		l.writeLine(i)
	}
	for k := ins; k < del; k++ {
		l.divs[at+ins].Call("remove")
		l.divs = slices.Delete(l.divs, at+ins, at+ins+1)
		l.drawn = slices.Delete(l.drawn, at+ins, at+ins+1)
	}
	switch {
	case ins == del:
	case !l.flow:
		for i := at + ins; i < len(l.divs); i++ {
			l.style(i)
		}
	case len(l.divs) > 0 && !l.divs[0].Equal(prev):
		// The first line is another element: it has the offset of the text's
		// top (style(0) is idempotent), and the one that had it, if it is still
		// in the layer, loses it.
		l.style(0)
		if prev.Truthy() && prev.Get("parentNode").Truthy() {
			prev.Call("setAttribute", "style", flowStyle(l.geom, false))
		}
	}
}

// point is the DOM position (node, offset) of the model offset off.
func (l *layer) point(off int) (js.Value, int) {
	line, unit := unitToLine(l.lines, off)
	if line >= len(l.divs) {
		return l.el, len(l.divs)
	}
	d := l.divs[line]
	for s := d.Get("firstChild"); s.Truthy(); s = s.Get("nextSibling") {
		t := s.Get("firstChild")
		if !t.Truthy() {
			continue
		}
		n := t.Get("length").Int()
		if unit <= n {
			return t, unit
		}
		unit -= n
	}
	return d, 0
}

// offsetOf is the model offset of a DOM position, or -1 when it is not inside
// the layer.
func (l *layer) offsetOf(node js.Value, off int) int {
	if !node.Truthy() || !l.el.Call("contains", node).Bool() {
		return -1
	}
	if node.Equal(l.el) {
		return lineToUnit(l.lines, off, 0)
	}
	line := node
	for line.Truthy() && !line.Get("parentNode").Equal(l.el) {
		line = line.Get("parentNode")
	}
	idx := -1
	for i, d := range l.divs {
		if d.Equal(line) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return -1
	}
	r := doc().Call("createRange")
	r.Call("selectNodeContents", line)
	r.Call("setEnd", node, off)
	return lineToUnit(l.lines, idx, textinput.Len(r.Call("toString").String()))
}

func (l *layer) focused() bool { return doc().Get("activeElement").Equal(l.el) }

// writeSelection sets the DOM selection from the model's, when the model's
// selection moved since the layer last looked (or force). It never writes while
// composing, and never overwrites a selection the browser made that
// selectionchange has not yet reported: the model has not moved then.
func (l *layer) writeSelection(force bool) {
	if l.composing || !l.focused() {
		return
	}
	a, f := l.c.InputSelection()
	if !force && l.hasSel && l.lastSel == [2]int{a, f} {
		return
	}
	l.lastSel, l.hasSel = [2]int{a, f}, true
	an, ao := l.point(a)
	fn, fo := l.point(f)
	doc().Call("getSelection").Call("setBaseAndExtent", an, ao, fn, fo)
}

// domState is what the DOM holds now, as a textinput.State: its text and
// selection, no mark. ok is false when the layer shows less than the model
// (bounded), when nothing may be deduced from it.
func (l *layer) domState() (textinput.State, bool) {
	if l.cut {
		return textinput.State{}, false
	}
	parts := make([]string, len(l.divs))
	for i, d := range l.divs {
		parts[i] = d.Get("textContent").String()
	}
	text := strings.Join(parts, "\n")
	sel := doc().Call("getSelection")
	a := l.offsetOf(sel.Get("anchorNode"), sel.Get("anchorOffset").Int())
	f := l.offsetOf(sel.Get("focusNode"), sel.Get("focusOffset").Int())
	end := textinput.Len(text)
	if a < 0 || f < 0 {
		a, f = end, end
	}
	return textinput.State{Text: text, Anchor: a, Focus: f, MarkStart: -1, MarkEnd: -1}, true
}

// after brings the DOM to the model after the scene handled something.
func (l *layer) after() {
	l.m.render()
	l.sync(l.node, l.scale)
}

func (l *layer) keydown(ev js.Value) {
	if ev.Get("defaultPrevented").Bool() { // host keys first
		return
	}
	key := ev.Get("key").String()
	ctrl, meta, alt := ev.Get("ctrlKey").Bool(), ev.Get("metaKey").Bool(), ev.Get("altKey").Bool()
	if routeKey(key, ctrl, meta, alt, ev.Get("isComposing").Bool(), ev.Get("keyCode").Int()) != keyScene {
		return
	}
	app := l.m.scene
	tellModifiers(app, Modifiers{Ctrl: ctrl, Shift: ev.Get("shiftKey").Bool(), Alt: alt, Meta: meta})
	tellKeyCode(app, keyCode(ev))
	if app.KeyDown(key) {
		ev.Call("preventDefault")
		l.after()
	}
}

func (l *layer) beforeinput(ev js.Value) {
	it := ev.Get("inputType").String()
	a := routeInput(it)
	switch a.Kind {
	case inputReplace:
		ev.Call("preventDefault")
		if l.c.InputReadOnly() {
			return
		}
		text := ""
		if it != "deleteByCut" && it != "deleteByDrag" {
			if d := ev.Get("data"); d.Type() == js.TypeString {
				text = d.String()
			} else if dt := ev.Get("dataTransfer"); dt.Truthy() {
				text = dt.Call("getData", "text/plain").String()
			}
		}
		l.c.ReplaceInput(-1, -1, text)
		l.after()
	case inputKey:
		ev.Call("preventDefault")
		if !l.c.InputReadOnly() || a.Key != "Enter" {
			l.m.scene.KeyDown(a.Key)
		}
		l.after()
	case inputOther:
		l.dirty = true
	}
}

func (l *layer) input(ev js.Value) {
	if l.composing || ev.Get("isComposing").Bool() {
		next, ok := l.domState()
		if !ok || l.c.InputReadOnly() {
			return
		}
		e := compositionEdit(textinput.StateOf(l.c), next)
		l.c.SetMarkedInput(e.Text, e.SelStart, e.SelEnd, e.ReplStart, e.ReplEnd)
		l.m.render()
		return
	}
	if !l.dirty {
		return
	}
	l.dirty = false
	if next, ok := l.domState(); ok && !l.c.InputReadOnly() {
		if e := textinput.Reconcile(textinput.StateOf(l.c), next); !e.IsZero() {
			l.c.ReplaceInput(e.Start, e.End, e.Text)
		}
	}
	l.hasRev = false // the DOM may differ from the model: rewrite it from the model
	l.hasG = false
	l.after()
}

func (l *layer) compositionend(js.Value) {
	l.composing = false
	l.c.UnmarkInput()
	l.hasRev, l.hasG, l.hasSel = false, false, false
	l.after()
	l.writeSelection(true)
}

func (l *layer) selectionchange(js.Value) {
	if l.composing || !l.focused() {
		return
	}
	sel := doc().Call("getSelection")
	a := l.offsetOf(sel.Get("anchorNode"), sel.Get("anchorOffset").Int())
	f := l.offsetOf(sel.Get("focusNode"), sel.Get("focusOffset").Int())
	if a < 0 || f < 0 {
		return
	}
	l.lastSel, l.hasSel = [2]int{a, f}, true
	if ma, mf := l.c.InputSelection(); ma != a || mf != f {
		l.c.SetInputSelection(a, f)
		l.m.render()
	}
}
