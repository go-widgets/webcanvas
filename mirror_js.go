// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package webcanvas

import "syscall/js"

// mirror is the DOM half of the accessibility mirror: a root element beside the
// canvas, holding two persistent live regions and one element per [A11yNode].
// The decisions (what to create, move, update; when to publish; what to
// announce) are in mirror.go; this file only applies them.
//
// Untrusted text: names, values and roles come from the app. They reach the DOM
// through textContent, setAttribute (a value, never a name taken from the app)
// and createElement of a tag ariaFor chose. There is no innerHTML, outerHTML,
// insertAdjacentHTML or document.write anywhere in this package.
type mirror struct {
	app    A11yAware
	canvas js.Value
	root   js.Value
	polite js.Value // role=status: polite announcements
	alert  js.Value // role=alert: assertive announcements
	click  func(x, y int)

	els   map[string]js.Value // key -> element
	count int                 // node elements under root (after the two regions)
	prev  frame
	has   bool
	byKey map[string]A11yNode // the published nodes, for activation
	focus string              // key of the focused node as published ("" none)
	th    throttle

	// keyAt is when a key press last activated a mirror element (performance.now).
	keyAt float64
	// force makes the next update publish (a resize).
	force bool
	// syncing is set while the glue itself moves DOM focus, so its own focus
	// event is not mistaken for assistive technology asking for focus.
	syncing bool
	timer   js.Value // pending setTimeout id, undefined when none
	timerFn js.Func
}

func nowMS() int64 { return int64(js.Global().Get("performance").Call("now").Float()) }

func doc() js.Value { return js.Global().Get("document") }

func newMirror(s *Surface, canvas js.Value, app A11yAware, click func(x, y int)) *mirror {
	m := &mirror{app: app, canvas: canvas, click: click,
		els: map[string]js.Value{}, byKey: map[string]A11yNode{}, timer: js.Undefined()}
	d := doc()
	m.root = d.Call("createElement", "div")
	m.root.Call("setAttribute", "data-webcanvas-mirror", "")
	m.root.Call("setAttribute", "tabindex", "0")
	// Both live regions exist, empty, before any text is put in them: a live
	// region created together with its content may not be announced. (Common
	// practice; WAI-ARIA 1.2 aria-live says only what polite and assertive mean.)
	m.polite = d.Call("createElement", "div")
	m.polite.Call("setAttribute", "role", "status")
	m.polite.Call("setAttribute", "aria-live", "polite")
	m.polite.Call("setAttribute", "aria-atomic", "true")
	m.alert = d.Call("createElement", "div")
	m.alert.Call("setAttribute", "role", "alert")
	m.alert.Call("setAttribute", "aria-live", "assertive")
	m.alert.Call("setAttribute", "aria-atomic", "true")
	m.root.Call("appendChild", m.polite)
	m.root.Call("appendChild", m.alert)
	m.place()
	if p := canvas.Get("parentNode"); p.Truthy() {
		p.Call("insertBefore", m.root, canvas.Get("nextSibling"))
	}

	m.timerFn = js.FuncOf(func(js.Value, []js.Value) any {
		m.timer = js.Undefined()
		guard(func() { m.update(false) })
		return nil
	})
	// An activation from assistive technology ("click", which is what a screen
	// reader's default action sends) is replayed as a press and release at the
	// node's centre, the way the native bridges do it: no second code path.
	on(&s.all, m.root, "click", nil, func(ev js.Value) {
		if isKeyboardEcho(ev.Get("detail").Int(), m.keyAt, js.Global().Get("performance").Call("now").Float()) {
			ev.Call("preventDefault")
			return // the browser's click for a key press already activated
		}
		if n, ok := m.nodeOf(ev.Get("target")); ok {
			x, y := centre(n)
			m.click(x, y)
		}
	})
	// Some browsers click a button on the keyup of Space: the keydown handler
	// has already activated, so cancel that keyup (its default action is none
	// in UI Events, and it is cancelable).
	on(&s.all, m.root, "keyup", nil, func(ev js.Value) {
		if ev.Get("key").String() == " " {
			if _, ok := m.nodeOf(ev.Get("target")); ok {
				ev.Call("preventDefault")
			}
		}
	})
	// A focus that assistive technology sends to a focusable node is a click,
	// so toolkit focus follows. Our own focus() calls are flagged and ignored.
	on(&s.all, m.root, "focusin", nil, func(ev js.Value) {
		if m.syncing {
			return
		}
		if n, ok := m.nodeOf(ev.Get("target")); ok && roleInteractive[n.Role] && !n.Focused {
			x, y := centre(n)
			m.click(x, y)
		}
	})
	// A click on the canvas focuses it (tabindex): hand the focus to the mirror,
	// where the keys are heard.
	on(&s.all, canvas, "focus", nil, func(js.Value) { m.syncFocus() })
	s.all.add(func() {}, func() {
		js.Global().Call("clearTimeout", m.timer)
		m.timerFn.Release()
		m.root.Call("remove")
	})
	return m
}

// nodeOf finds the published node whose element is target or contains it.
func (m *mirror) nodeOf(target js.Value) (A11yNode, bool) {
	for el := target; el.Truthy() && !el.Equal(m.root); el = el.Get("parentNode") {
		if k := el.Get("wcKey"); k.Type() == js.TypeString {
			n, ok := m.byKey[k.String()]
			return n, ok
		}
	}
	return A11yNode{}, false
}

// scale is CSS pixels per surface pixel: the canvas' displayed width over its
// backing width, so it holds under CSS resizing and a device pixel ratio alike.
func (m *mirror) scale() float64 {
	w := m.canvas.Get("width").Float()
	if w <= 0 {
		return 1
	}
	return m.canvas.Get("clientWidth").Float() / w
}

// place puts the root over the canvas.
func (m *mirror) place() {
	m.root.Call("setAttribute", "style", rootBox(m.canvas.Get("offsetLeft").Float(), m.canvas.Get("offsetTop").Float(),
		m.canvas.Get("clientWidth").Float(), m.canvas.Get("clientHeight").Float()))
}

func focusKey(ns []A11yNode) string {
	for _, n := range ns {
		if n.Focused {
			return n.Key
		}
	}
	return ""
}

// update is called after every frame: it reads the scene's tree and, as the
// throttle allows, brings the DOM to it. A change refused as too soon is
// deferred, so the DOM always catches up to the last tree.
func (m *mirror) update(force bool) {
	// Never build more than maxMirrorNodes elements, whatever the scene returns.
	nodes, _ := capNodes(m.app.A11yNodes(), maxMirrorNodes)
	scale := m.scale()
	sig := treeSig(nodes, scale)
	// Focus is the one thing a user waits on: a change of the focused node is
	// never held back.
	force = force || m.force || focusKey(nodes) != m.focus
	act, wait := m.th.decide(sig, force, nowMS())
	switch act {
	case actSkip:
		return
	case actDefer:
		if m.timer.IsUndefined() {
			m.timer = js.Global().Call("setTimeout", m.timerFn, int(wait))
		}
		return
	}
	m.force = false
	if !m.timer.IsUndefined() {
		js.Global().Call("clearTimeout", m.timer)
		m.timer = js.Undefined()
	}
	m.apply(nodes, scale)
	m.th.published(sig, nowMS())
}

func (m *mirror) apply(nodes []A11yNode, scale float64) {
	next := frame{Nodes: nodes, Scale: scale}
	var before []A11yNode
	if m.has {
		before = m.prev.Nodes
		if before == nil {
			before = []A11yNode{}
		}
	}
	ann := announce(before, nodes)
	m.place()
	for _, o := range mirrorPlan(m.prev, next) {
		m.run(o)
	}
	m.prev, m.has = next, true
	m.byKey = make(map[string]A11yNode, len(nodes))
	for _, n := range dedupe(nodes) {
		m.byKey[n.Key] = n
	}
	m.focus = focusKey(nodes)
	if ann.Polite != "" {
		m.polite.Set("textContent", ann.Polite)
	}
	if ann.Assertive != "" {
		m.alert.Set("textContent", ann.Assertive)
	}
	m.syncFocus()
}

// syncFocus makes DOM focus follow toolkit focus (design decision 2): the
// mirror element of the focused node holds real DOM focus, else the root does.
// It moves focus only when the surface already has it, so a page's other
// controls are never robbed.
func (m *mirror) syncFocus() {
	active := doc().Get("activeElement")
	if !active.Truthy() || !(active.Equal(m.root) || m.root.Call("contains", active).Bool() || active.Equal(m.canvas)) {
		return
	}
	want := m.root
	if el, ok := m.els[m.focus]; ok && m.focus != "" {
		want = el
	}
	if active.Equal(want) {
		return
	}
	m.syncing = true
	want.Call("focus", map[string]any{"preventScroll": true})
	m.syncing = false
}

// elementFor builds the element of a create op.
//
// W3 hook: this is the one place where a focused role=textbox node will be
// given the text-input bridge's element (its editing host) instead of a plain
// <div role=textbox>; nothing else in the mirror knows about it.
func (m *mirror) elementFor(o op) js.Value {
	el := doc().Call("createElement", o.Spec.Tag)
	el.Set("wcKey", o.Key)
	m.setSpec(el, o.Spec.Attrs, nil, o.Spec.Text, true)
	return el
}

func (m *mirror) setSpec(el js.Value, set map[string]string, del []string, text string, setText bool) {
	for k, v := range set {
		el.Call("setAttribute", k, v)
	}
	for _, k := range del {
		el.Call("removeAttribute", k)
	}
	if setText {
		el.Set("textContent", text)
	}
}

// at is the element currently at node index i, or null.
func (m *mirror) at(i int) js.Value { return m.root.Get("children").Index(2 + i) }

func (m *mirror) run(o op) {
	switch o.Kind {
	case opRemove:
		m.els[o.Key].Call("remove")
		delete(m.els, o.Key)
		m.count--
	case opCreate:
		el := m.elementFor(o)
		m.els[o.Key] = el
		if o.Index >= m.count {
			m.root.Call("appendChild", el)
		} else {
			m.root.Call("insertBefore", el, m.at(o.Index))
		}
		m.count++
	case opMove:
		el := m.els[o.Key]
		el.Call("remove")
		if o.Index >= m.count-1 {
			m.root.Call("appendChild", el)
		} else {
			m.root.Call("insertBefore", el, m.at(o.Index))
		}
	case opUpdate:
		m.setSpec(m.els[o.Key], o.Set, o.Del, o.Spec.Text, o.SetText)
	}
}
