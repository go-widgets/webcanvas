// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import "github.com/go-widgets/painter"

// This file is the pure half of the incremental present: which pixels a frame
// must put on the canvas and which layers it must redraw, decided from the
// scene's damage and the layers of this frame and the last. run_js.go supplies
// the canvas ([canvasTarget]); tests supply a software one, so the whole
// decision is checked natively, pixel for pixel against a full repaint.

// Rect is a rectangle in canvas pixels. It is painter.Rect, which is also
// toolkit.Rect: the type go-widgets/window's DamageRenderer and toolkit's
// Surface.Damage already use, so a scene reports damage in one vocabulary.
type Rect = painter.Rect

// DamageDrawer is an optional companion to [App]: a scene that knows what its
// last frame changed implements it, and the host then copies and puts only
// those rectangles on the canvas instead of the whole framebuffer.
//
// It is the browser's form of the contract go-widgets/window's DamageRenderer
// (RenderDamaged(p, th) []toolkit.Rect) gives a desktop host, with the same
// meaning: the scene repaints only what changed into a framebuffer that
// PERSISTS between frames, and returns the rectangles it repainted, in canvas
// pixels; an EMPTY result means nothing changed, and the host puts nothing.
// (toolkit.Surface.Damage differs: there nil means "everything", so it cannot
// say "nothing", which is the frame that costs least.) RenderDamaged itself
// does not fit: it is handed a painter.Painter and a *toolkit.Theme, while
// [App.Draw] gets a bare RGBA buffer and webcanvas owns no theme.
//
// The host calls [App.Draw] (the whole scene) for the first frame and after
// every resize, then DrawDamaged. A change omitted from the result is a change
// the canvas never shows, so return every rectangle whose pixels differ from
// the previous frame; a scene that cannot tell leaves the interface off, or
// answers with a rectangle covering the canvas. It must also do the work the
// frame owes (draining a queue, say), as Draw does.
type DamageDrawer interface {
	App
	DrawDamaged(buf []byte) []Rect
}

// target is what a frame is put on: the canvas, or a test's software canvas.
type target interface {
	// Put copies the pixels of the scene buffer inside r onto the canvas.
	Put(r Rect)
	// DrawLayer draws one layer over what the canvas holds.
	DrawLayer(l Layer)
}

// composer remembers the last frame's layers, which decide what the next frame
// must restore.
type composer struct {
	prev []Layer
	// full forces the next frame to be a whole one: no frame yet, or a resize.
	full bool
}

func newComposer() *composer { return &composer{full: true} }

// reset makes the next frame a whole one (the framebuffer was reallocated).
func (c *composer) reset() { c.full = true }

// frame paints one frame of app, whose scene buffer is buf (w×h), on t.
//
// A [DamageDrawer] scene that is not on its first frame repaints only its
// damage, and the canvas gets only those rectangles, plus the rectangles
// layers left or now need; every layer is then drawn again, since the image
// behind a handle can change without the handle doing so. A frame with no
// damage and only opaque layers therefore puts nothing on the canvas.
// Any other scene is drawn whole and put whole, as before.
func (c *composer) frame(app App, buf []byte, w, h int, t target) {
	whole := Rect{W: w, H: h}
	var dirty []Rect
	dd, damageAware := app.(DamageDrawer)
	if damageAware && !c.full {
		dirty = clipRects(dd.DrawDamaged(buf), w, h)
	} else {
		app.Draw(buf)
		dirty = []Rect{whole}
		c.full = false
	}
	layers := layersOf(app, w, h)
	if len(dirty) == 1 && dirty[0] == whole {
		t.Put(whole)
	} else {
		dirty = append(dirty, layerRestore(c.prev, layers, w, h)...)
		for _, r := range coalesce(dirty) {
			t.Put(r)
		}
	}
	for _, l := range layers {
		t.DrawLayer(l)
	}
	c.prev = layers
}

// layerRect is the part of l on the w×h canvas.
func layerRect(l Layer, w, h int) Rect {
	return clip(Rect{X: l.X, Y: l.Y, W: l.W, H: l.H}, w, h)
}

// layerRestore is the part of the canvas whose scene pixels must be put back
// before the layers are drawn again: what a layer drawn last frame covered
// that no opaque layer covers now (it moved, shrank or went), and what a layer
// that is not opaque will blend over (drawing it again over its own previous
// frame would stack it).
func layerRestore(prev, cur []Layer, w, h int) []Rect {
	var out []Rect
	for _, l := range cur {
		if !l.Opaque || l.Alpha != 1 {
			out = append(out, layerRect(l, w, h))
		}
	}
	for _, p := range prev {
		r := layerRect(p, w, h)
		covered := false
		for _, l := range cur {
			if l.Opaque && l.Alpha == 1 && contains(layerRect(l, w, h), r) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out
}

// clip is r confined to the w×h canvas; empty when they do not meet.
func clip(r Rect, w, h int) Rect {
	x0, y0 := max(r.X, 0), max(r.Y, 0)
	x1, y1 := min(r.X+r.W, w), min(r.Y+r.H, h)
	if x1 <= x0 || y1 <= y0 {
		return Rect{}
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// clipRects confines every rectangle to the canvas and drops the empty ones.
func clipRects(rs []Rect, w, h int) []Rect {
	out := make([]Rect, 0, len(rs))
	for _, r := range rs {
		if r = clip(r, w, h); r.W > 0 {
			out = append(out, r)
		}
	}
	return out
}

// contains reports whether outer covers inner; an empty inner is covered.
func contains(outer, inner Rect) bool {
	if inner.W <= 0 || inner.H <= 0 {
		return true
	}
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.X+inner.W <= outer.X+outer.W && inner.Y+inner.H <= outer.Y+outer.H
}

// coalesce drops the empty rectangles and every rectangle another one covers.
func coalesce(rs []Rect) []Rect {
	var out []Rect
next:
	for i, r := range rs {
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		for j, o := range rs {
			if i != j && contains(o, r) && (o != r || j < i) {
				continue next
			}
		}
		out = append(out, r)
	}
	return out
}
