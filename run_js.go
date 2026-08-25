// Copyright (c) 2026 the go-widgets/gallery authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

//go:build js && wasm

package webcanvas

import "syscall/js"

// Run boots app onto the <canvas> whose id is screenID and never returns: it
// sizes the canvas from [App.Size], blits [App.Draw] into it, and wires the DOM
// event listeners that drive [App.Click]/Move/Release/Context/Char/KeyDown.
// When app also implements [Ticker], a 60-Hz timer repaints on every tick; when
// it implements [Animator], a requestAnimationFrame loop drives it; when it
// implements [Resizer], the canvas tracks the viewport and the scene relayouts on
// every window resize.
//
// Every DOM callback runs inside [guard], a recover net: a panic escaping one
// handler (or the paint it triggers) is logged through [PanicReporter] and
// swallowed, so a single bad frame never blanks the whole instance and the next
// event still dispatches. It is a last-resort net on top of the toolkit's own
// per-widget hardening, not a substitute for it.
//
// This is the module's ONE canvas host. It carries no widget knowledge, so
// every demo — the widget gallery, the isometric editor — reuses it unchanged
// by supplying an App. A missing canvas is reported and Run returns, leaving
// the page intact (the host shell shows its own boot-error text).
func Run(screenID string, app App) {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", screenID)
	if canvas.IsUndefined() || canvas.IsNull() {
		println("webcanvas: no #" + screenID + " canvas in the host page")
		return
	}
	// w, h, and the framebuffer trio are mutable: a [Resizer] scene reallocates
	// them on every viewport change, and coords/render read the live values.
	w, h := app.Size()
	canvas.Set("width", w)
	canvas.Set("height", h)
	ctx := canvas.Call("getContext", "2d")

	local := make([]byte, 4*w*h)
	imageData := ctx.Call("createImageData", w, h)
	dst := imageData.Get("data")

	render := func() {
		app.Draw(local)
		js.CopyBytesToJS(dst, local)
		ctx.Call("putImageData", imageData, 0, 0)
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
	// listen wires a pointer event whose handler takes canvas-local coords and
	// reports whether to repaint. The whole body (coords → handle → render) runs
	// under the guard net, so a panic anywhere in it is logged, not fatal.
	listen := func(name string, handle func(x, y int) bool) {
		cb := js.FuncOf(func(_ js.Value, args []js.Value) any {
			guard(func() {
				if len(args) == 0 {
					return
				}
				x, y := coords(args[0])
				if handle(x, y) {
					render()
				}
			})
			return nil
		})
		canvas.Call("addEventListener", name, cb)
	}
	// Left-button press only: a right press is the context menu's business.
	canvas.Call("addEventListener", "mousedown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		guard(func() {
			if len(args) == 0 || args[0].Get("button").Int() != 0 {
				return
			}
			x, y := coords(args[0])
			if app.Click(x, y) {
				render()
			}
		})
		return nil
	}))
	listen("mousemove", app.Move)
	listen("mouseup", app.Release)
	// Right-click opens the context menu; suppress the browser's own menu.
	canvas.Call("addEventListener", "contextmenu", js.FuncOf(func(_ js.Value, args []js.Value) any {
		guard(func() {
			if len(args) == 0 {
				return
			}
			args[0].Call("preventDefault")
			x, y := coords(args[0])
			if app.Context(x, y) {
				render()
			}
		})
		return nil
	}))
	// Keyboard on the window (a <canvas> is not focusable). A single printable
	// rune with no Ctrl/Meta/Alt is text (Char); every named or modified key is
	// a KeyDown.
	js.Global().Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		guard(func() {
			if len(args) == 0 {
				return
			}
			ev := args[0]
			key := ev.Get("key").String()
			var changed bool
			if len([]rune(key)) == 1 && !ev.Get("ctrlKey").Bool() && !ev.Get("metaKey").Bool() && !ev.Get("altKey").Bool() {
				changed = app.Char(key)
			} else {
				changed = app.KeyDown(key)
			}
			if changed {
				ev.Call("preventDefault")
				render()
			}
		})
		return nil
	}))

	// Optional wheel scrolling: a [Scroller] scene gets the browser's wheel routed
	// to the widget under the pointer as toolkit scroll ROWS (deltaX/deltaY
	// normalised for the event's deltaMode). preventDefault stops the page itself
	// from scrolling under the canvas, so the wheel reaches the scene's scrollable
	// region instead of the document. A scene that does not implement Scroller
	// installs no listener, so the page's default wheel handling — and every
	// existing demo — is untouched.
	if sc, ok := app.(Scroller); ok {
		wheel := js.FuncOf(func(_ js.Value, args []js.Value) any {
			guard(func() {
				if len(args) == 0 {
					return
				}
				ev := args[0]
				ev.Call("preventDefault")
				x, y := coords(ev)
				mode := ev.Get("deltaMode").Int()
				dx := scrollRows(ev.Get("deltaX").Float(), mode)
				dy := scrollRows(ev.Get("deltaY").Float(), mode)
				if sc.Scroll(x, y, dx, dy) {
					render()
				}
			})
			return nil
		})
		// passive:false is required for preventDefault to take effect on a wheel
		// listener (browsers treat wheel as passive by default).
		canvas.Call("addEventListener", "wheel", wheel, map[string]any{"passive": false})
	}

	// Optional viewport tracking: a [Resizer] scene fills the browser window and
	// relayouts on every resize. resize reads the canvas' laid-out client box,
	// asks the scene to relayout to it (which returns the size it will render at),
	// reallocates the framebuffer trio to match, and repaints. A scene that does
	// not implement Resizer installs no listener and keeps its fixed Size forever
	// (the gallery's behaviour is unchanged).
	if rz, ok := app.(Resizer); ok {
		resize := func() {
			cw := canvas.Get("clientWidth").Int()
			ch := canvas.Get("clientHeight").Int()
			if cw <= 0 || ch <= 0 {
				return
			}
			w, h = rz.Resize(cw, ch)
			canvas.Set("width", w)
			canvas.Set("height", h)
			local = make([]byte, 4*w*h)
			imageData = ctx.Call("createImageData", w, h)
			dst = imageData.Get("data")
			render()
		}
		js.Global().Call("addEventListener", "resize", js.FuncOf(func(_ js.Value, _ []js.Value) any {
			guard(resize)
			return nil
		}))
		// Fit the canvas to the viewport once, now, so the app opens full-page
		// instead of at its intrinsic Size().
		guard(resize)
	}

	// Optional fixed-cadence clock: only scenes with time-varying state ask for it.
	if t, ok := app.(Ticker); ok {
		tick := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			guard(func() {
				t.Tick()
				render()
			})
			return nil
		})
		js.Global().Call("setInterval", tick, 16)
	}

	// Optional real-clock animation via requestAnimationFrame: hand the scene the
	// true elapsed dt (seconds) between frames and repaint only when it reports a
	// visible change, so an idle scene costs nothing beyond the rAF callback.
	if a, ok := app.(Animator); ok {
		var prevMS float64
		var raf js.Func
		raf = js.FuncOf(func(_ js.Value, args []js.Value) any {
			guard(func() {
				nowMS := args[0].Float()
				if prevMS != 0 && a.AnimationStep((nowMS-prevMS)/1000) {
					render()
				}
				prevMS = nowMS
			})
			js.Global().Call("requestAnimationFrame", raf)
			return nil
		})
		js.Global().Call("requestAnimationFrame", raf)
	}

	// Park forever so the callbacks live.
	select {}
}
