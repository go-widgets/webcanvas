// Copyright (c) 2026 the go-widgets/gallery authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

//go:build js && wasm

package webcanvas

import (
	"sync/atomic"
	"syscall/js"
)

// keyCode is a keyboard event's code ("" when the browser sets none).
func keyCode(ev js.Value) string {
	if c := ev.Get("code"); c.Type() == js.TypeString {
		return c.String()
	}
	return ""
}

// Run boots app onto the <canvas> whose id is screenID and never returns: it is
// [Mount] on that canvas, focus on it, and a park. Use Mount for a page that
// holds several canvases or must keep running code after the scene is up.
//
// A missing canvas is reported and Run returns, leaving the page intact (the
// host shell shows its own boot-error text).
func Run(screenID string, app App) { RunWith(screenID, app, nil) }

// Presenter puts frames on the canvas in place of the default, which paints
// App.Draw's RGBA buffer through a 2D context. Package webcanvas/gpu has one
// that draws on the GPU.
//
// A canvas takes ONE kind of context for life, so the choice is made once,
// before the first frame: Attach is handed the untouched canvas, already
// w × h, and returns whether it takes it. A presenter that declines must not
// have asked the canvas for a context; Run then paints through 2D as usual.
// One that accepts draws every frame from then on, at the canvas' current
// size. A [Layered] scene is not offered to a presenter: its images are drawn
// through the 2D context.
type Presenter interface {
	Attach(canvas js.Value, app App, w, h int) bool
	Frame(app App, w, h int)
}

// RunWith is Run with a Presenter; nil, or a presenter that declines, is Run.
func RunWith(screenID string, app App, p Presenter) {
	canvas := js.Global().Get("document").Call("getElementById", screenID)
	if canvas.IsUndefined() || canvas.IsNull() {
		println("webcanvas: no #" + screenID + " canvas in the host page")
		return
	}
	s := MountWith(canvas, app, p)
	// One canvas on the page: it owns the keyboard from the start, as it did
	// when the listeners were on the window.
	s.Focus()
	select {} // park forever so the callbacks live
}

// Surface is one mounted canvas: its scene, its framebuffer and every listener,
// timer and animation frame it installed. Surfaces are independent; a page may
// hold as many as it likes, and Close removes one without touching the others.
type Surface struct {
	canvas js.Value
	// keyTarget is the element that receives this surface's keyboard events.
	// Today it is the canvas itself; the accessibility mirror (#21, W2) rebinds
	// it to its mirror root through bindKeys, with no change to the API.
	keyTarget js.Value
	all       listeners // everything but the keyboard
	keys      listeners // the keyboard group, rebound by bindKeys
	refresh   func()
}

// Refresh draws a frame now and, for an [A11yAware] scene, updates the
// accessibility mirror with it (subject to the 200 ms throttle). Input events
// and the scene's own repaint requests already do this; Refresh is for a host
// that changed the scene from outside.
func (s *Surface) Refresh() { s.refresh() }

// Canvas is the <canvas> this surface draws on.
func (s *Surface) Canvas() js.Value { return s.canvas }

// Focus gives the keyboard to this surface: keys typed from now on reach its
// scene, and no other surface's. The browser does the same when the canvas is
// clicked or tabbed to.
func (s *Surface) Focus() {
	s.keyTarget.Call("focus", map[string]any{"preventScroll": true})
}

// Close removes every listener, timer and animation frame the surface
// installed. The canvas keeps its last frame. Close is idempotent.
func (s *Surface) Close() {
	s.keys.close()
	s.all.close()
}

// on attaches fn to target for the event name through the ledger l; Close (or a
// rebind) removes the listener and releases its js.Func. fn runs under [guard].
func on(l *listeners, target js.Value, name string, opts any, fn func(ev js.Value)) {
	var cb js.Func
	cb = js.FuncOf(func(_ js.Value, args []js.Value) any {
		guard(func() {
			ev := js.Undefined()
			if len(args) > 0 {
				ev = args[0]
			}
			fn(ev)
		})
		return nil
	})
	l.add(func() {
		if opts == nil {
			target.Call("addEventListener", name, cb)
		} else {
			target.Call("addEventListener", name, cb, opts)
		}
	}, func() {
		target.Call("removeEventListener", name, cb)
		cb.Release()
	})
}

// Mount puts app on canvas and returns at once. Every listener is the
// surface's own: pointer and wheel events on the canvas, keys on the element
// that has focus (the canvas, made focusable with tabindex when the page gave
// it none), resize on the window. Two surfaces on one page therefore never see
// each other's input: a key reaches only the surface that has focus, and the
// wheel the one under the pointer.
//
// Mount does not take focus; call [Surface.Focus]. See [Run] for the single
// canvas that does.
func Mount(canvas js.Value, app App) *Surface { return MountWith(canvas, app, nil) }

// MountWith is Mount with a [Presenter]; nil, or one that declines, is Mount.
// A Presenter has no teardown of its own, so Close stops what Mount installed,
// not what the presenter holds.
func MountWith(canvas js.Value, app App, p Presenter) *Surface {
	s := &Surface{canvas: canvas, keyTarget: canvas}
	aware, hasMirror := app.(A11yAware)
	if !canvas.Call("hasAttribute", "tabindex").Bool() {
		// With a mirror the mirror root is the tab stop and hears the keys; the
		// canvas stays focusable by click only and hands its focus over.
		ti := "0"
		if hasMirror {
			ti = "-1"
		}
		canvas.Call("setAttribute", "tabindex", ti)
	}
	// w, h, and the framebuffer trio are mutable: a [Resizer] scene reallocates
	// them on every viewport change, and coords/render read the live values.
	w, h := app.Size()
	canvas.Set("width", w)
	canvas.Set("height", h)

	presented := false
	if _, layered := app.(Layered); p != nil && !layered {
		guard(func() { presented = p.Attach(canvas, app, w, h) })
	}
	var (
		ctx, imageData, dst js.Value
		local               []byte
	)
	// alloc sizes the 2D path's framebuffer trio; a presented canvas has none.
	alloc := func() {
		if presented {
			return
		}
		if ctx.IsUndefined() {
			ctx = canvas.Call("getContext", "2d")
		}
		local = make([]byte, 4*w*h)
		imageData = ctx.Call("createImageData", w, h)
		dst = imageData.Get("data")
	}
	alloc()

	var closed atomic.Bool
	var mir *mirror // nil unless the scene is an [A11yAware]
	var render func()
	// paint draws one frame; render is paint plus the mirror's update.
	paint := func() {
		if closed.Load() {
			return
		}
		if presented {
			p.Frame(app, w, h)
			return
		}
		app.Draw(local)
		js.CopyBytesToJS(dst, local)
		ctx.Call("putImageData", imageData, 0, 0)
		// A [Layered] scene's images go over the frame, each under its own
		// net: a closed ImageBitmap throws, and costs its own layer only.
		for _, l := range layersOf(app, w, h) {
			v, ok := l.Image.Handle().(js.Value)
			if !ok {
				continue
			}
			guard(func() {
				ctx.Set("globalAlpha", l.Alpha)
				ctx.Set("imageSmoothingEnabled", true)
				ctx.Set("imageSmoothingQuality", "high")
				ctx.Call("drawImage", v, l.X, l.Y, l.W, l.H)
			})
			ctx.Set("globalAlpha", 1)
		}
	}
	render = func() {
		paint()
		if mir != nil && !closed.Load() {
			mir.update(false)
		}
	}
	s.refresh = func() { guard(render) }
	if hasMirror {
		mir = newMirror(s, canvas, aware, func(x, y int) {
			// What a screen reader's activation sends: a press and release.
			c := app.Click(x, y)
			r := app.Release(x, y)
			if c || r {
				render()
			}
		})
	}
	// A [RepaintAware] scene gets a way to ask for a frame from any goroutine.
	// One frame is in flight at most: the flag is cleared when it is drawn.
	if ra, ok := app.(RepaintAware); ok {
		var (
			pending atomic.Bool
			rafID   atomic.Int64
		)
		frame := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			pending.Store(false)
			guard(render)
			return nil
		})
		ra.RepaintWith(func() {
			if !closed.Load() && pending.CompareAndSwap(false, true) {
				rafID.Store(int64(js.Global().Call("requestAnimationFrame", frame).Int()))
			}
		})
		s.all.add(func() {}, func() {
			if pending.Load() {
				js.Global().Call("cancelAnimationFrame", int(rafID.Load()))
			}
			frame.Release()
		})
	}
	render()
	// Signal the host page that the first frame is painted, so a loading
	// placeholder can reveal the canvas. Purely additive — it changes no
	// rendering, so every existing demo behaves exactly as before.
	js.Global().Set("webcanvasReady", true)

	// clientX/Y → canvas-local pixel coords via the bounding-rect, scaling for
	// any CSS resize (the canvas keeps its intrinsic w*h while displayed at an
	// arbitrary size), so the mapping stays exact when the page shrinks it.
	coords := func(ev js.Value) (int, int) {
		rect := canvas.Call("getBoundingClientRect")
		sx := rect.Get("width").Float() / float64(w)
		sy := rect.Get("height").Float() / float64(h)
		x := int((ev.Get("clientX").Float() - rect.Get("left").Float()) / sx)
		y := int((ev.Get("clientY").Float() - rect.Get("top").Float()) / sy)
		return x, y
	}
	// mods reads the DOM event's modifier flags for a [ModifierAware] scene.
	mods := func(ev js.Value) Modifiers {
		return Modifiers{Ctrl: ev.Get("ctrlKey").Bool(), Shift: ev.Get("shiftKey").Bool(),
			Alt: ev.Get("altKey").Bool(), Meta: ev.Get("metaKey").Bool()}
	}
	// pointer wires a pointer event whose handler takes canvas-local coords and
	// reports whether to repaint.
	pointer := func(name string, handle func(x, y int) bool) {
		on(&s.all, canvas, name, nil, func(ev js.Value) {
			tellModifiers(app, mods(ev))
			x, y := coords(ev)
			if handle(x, y) {
				render()
			}
		})
	}
	// Left-button press only: a right press is the context menu's business.
	on(&s.all, canvas, "mousedown", nil, func(ev js.Value) {
		if ev.Get("button").Int() != 0 {
			return
		}
		tellModifiers(app, mods(ev))
		x, y := coords(ev)
		if app.Click(x, y) {
			render()
		}
	})
	pointer("mousemove", app.Move)
	pointer("mouseup", app.Release)
	// Right-click opens the context menu; suppress the browser's own menu.
	on(&s.all, canvas, "contextmenu", nil, func(ev js.Value) {
		ev.Call("preventDefault")
		tellModifiers(app, mods(ev))
		x, y := coords(ev)
		if app.Context(x, y) {
			render()
		}
	})

	// Keyboard, on the key target: only the focused surface hears a key. A
	// single printable rune with no Ctrl/Meta/Alt is text (Char); every named
	// or modified key is a KeyDown. bindKeys moves the whole group to another
	// element (the accessibility mirror root, later) and back.
	ku, _ := app.(KeyUpper)
	bindKeys := func(target js.Value) {
		s.keys.clear()
		s.keyTarget = target
		on(&s.keys, target, "keydown", nil, func(ev js.Value) {
			tellModifiers(app, mods(ev))
			tellKeyCode(app, keyCode(ev))
			key := ev.Get("key").String()
			var changed bool
			if isTextKey(key, ev.Get("ctrlKey").Bool(), ev.Get("metaKey").Bool(), ev.Get("altKey").Bool()) {
				changed = app.Char(key)
			} else {
				changed = app.KeyDown(key)
			}
			if changed {
				ev.Call("preventDefault")
				render()
			}
		})
		// Optional key releases: a [KeyUpper] scene is told when a key goes up,
		// with its modifiers and code first, as for a key going down.
		if ku != nil {
			on(&s.keys, target, "keyup", nil, func(ev js.Value) {
				tellModifiers(app, mods(ev))
				tellKeyCode(app, keyCode(ev))
				if ku.KeyUp(ev.Get("key").String()) {
					ev.Call("preventDefault")
					render()
				}
			})
		}
	}
	if mir != nil {
		bindKeys(mir.root) // the focused mirror element is where keys land
	} else {
		bindKeys(canvas)
	}

	// Optional wheel: a [Scroller] always consumes it; a [ScrollConsumer] says
	// per event (see its doc for the passive-listener reasoning). The listener is
	// installed only for such a scene, so the page's default wheel handling is
	// untouched otherwise. {passive:false}: without it preventDefault is a no-op
	// wherever the browser defaults wheel listeners to passive.
	if hasWheel(app) {
		on(&s.all, canvas, "wheel", map[string]any{"passive": false}, func(ev js.Value) {
			tellModifiers(app, mods(ev))
			x, y := coords(ev)
			mode := ev.Get("deltaMode").Int()
			dx := scrollRows(ev.Get("deltaX").Float(), mode)
			dy := scrollRows(ev.Get("deltaY").Float(), mode)
			r := routeWheel(app, x, y, dx, dy)
			if r.PreventDefault {
				ev.Call("preventDefault")
			}
			if r.Repaint {
				render()
			}
		})
	}

	// Optional viewport tracking: a [Resizer] scene fills the browser window and
	// relayouts on every resize. resize reads the canvas' laid-out client box,
	// asks the scene to relayout to it (which returns the size it will render at),
	// reallocates the framebuffer trio to match, and repaints. A scene that does
	// not implement Resizer installs no listener and keeps its fixed Size forever
	// (the gallery's behaviour is unchanged).
	if rz, ok := app.(Resizer); ok {
		// A [PixelRatioAware] scene renders at the display's device pixels: the
		// backing store is the CSS box × devicePixelRatio (the page keeps the
		// CSS size), so a Retina display shows crisp pixels instead of a 1×
		// bitmap the browser upscales. Pointer coordinates already map through
		// the bounding rect onto the backing store (see coords).
		pr, _ := app.(PixelRatioAware)
		ratio := 1.0
		resize := func() {
			cw := canvas.Get("clientWidth").Int()
			ch := canvas.Get("clientHeight").Int()
			if cw <= 0 || ch <= 0 {
				return
			}
			if pr != nil {
				ratio = normalRatio(js.Global().Get("devicePixelRatio").Float())
				pr.SetPixelRatio(ratio)
			}
			w, h = rz.Resize(devicePixels(cw, ratio), devicePixels(ch, ratio))
			canvas.Set("width", w)
			canvas.Set("height", h)
			alloc()
			if mir != nil {
				mir.force = true // a new size is never throttled
			}
			render()
		}
		on(&s.all, js.Global(), "resize", nil, func(js.Value) { resize() })
		// Fit the canvas to the viewport once, now, so the app opens full-page
		// instead of at its intrinsic Size().
		guard(resize)
		if pr != nil {
			// The ratio changes with browser zoom and when the window moves to a
			// display of another density; a media query on the current ratio
			// fires once when it stops matching, then the next one is installed
			// (MDN, Window.devicePixelRatio).
			var (
				stopWatch func()
				watch     func()
			)
			watch = func() {
				if closed.Load() {
					return
				}
				mq := js.Global().Call("matchMedia", resolutionQuery(ratio))
				var cb js.Func
				cb = js.FuncOf(func(_ js.Value, _ []js.Value) any {
					mq.Call("removeEventListener", "change", cb)
					cb.Release()
					guard(resize)
					watch()
					return nil
				})
				mq.Call("addEventListener", "change", cb)
				stopWatch = func() {
					mq.Call("removeEventListener", "change", cb)
					cb.Release()
				}
			}
			s.all.add(watch, func() {
				if stopWatch != nil {
					stopWatch()
				}
			})
		}
	}

	// Optional fixed-cadence clock: only scenes with time-varying state ask for it.
	if t, ok := app.(Ticker); ok {
		var id js.Value
		var tick js.Func
		tick = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			guard(func() {
				t.Tick()
				render()
			})
			return nil
		})
		s.all.add(func() { id = js.Global().Call("setInterval", tick, 16) }, func() {
			js.Global().Call("clearInterval", id)
			tick.Release()
		})
	}

	// Optional real-clock animation via requestAnimationFrame: hand the scene the
	// true elapsed dt (seconds) between frames and repaint only when it reports a
	// visible change, so an idle scene costs nothing beyond the rAF callback.
	if a, ok := app.(Animator); ok {
		var (
			prevMS float64
			id     js.Value
			raf    js.Func
		)
		raf = js.FuncOf(func(_ js.Value, args []js.Value) any {
			if closed.Load() {
				return nil
			}
			guard(func() {
				nowMS := args[0].Float()
				if prevMS != 0 && a.AnimationStep((nowMS-prevMS)/1000) {
					render()
				}
				prevMS = nowMS
			})
			id = js.Global().Call("requestAnimationFrame", raf)
			return nil
		})
		s.all.add(func() { id = js.Global().Call("requestAnimationFrame", raf) }, func() {
			js.Global().Call("cancelAnimationFrame", id)
			raf.Release()
		})
	}

	// Closing marks the surface first, so a frame already queued draws nothing.
	s.all.add(func() {}, func() { closed.Store(true) })
	return s
}
