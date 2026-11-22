// SPDX-License-Identifier: BSD-3-Clause

// Package gpu is a webcanvas.Presenter that draws on the GPU through WebGPU.
//
// An App that can paint itself through a painter.Painter ([Painted]) is
// recorded by go-widgets/gpupainter into a go-gfx/gpu2d list; gpu2d's WebGPU
// renderer draws the list and copies the frame into the canvas texture. The
// frame never passes through Go memory or putImageData, and the pixels are
// PixelPainter's (gpupainter and gpu2d hold that by test).
//
//	webcanvas.RunWith("screen", app, gpu.New())
//
// The presenter takes the canvas only once it holds a device, a renderer and
// a surface; until then it has not touched the canvas, so a browser without
// WebGPU, or an App that is not [Painted], falls back to webcanvas' 2D path.
// Err says why.
package gpu

import (
	"errors"

	"github.com/go-gfx/gpu2d"
	"github.com/go-widgets/gpupainter"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/webcanvas"
)

// Painted is an App that paints its whole frame, background included,
// through a painter.Painter, as it would into a PixelPainter in Draw.
type Painted interface {
	webcanvas.App
	Paint(p painter.Painter)
}

// DamagePainted is a [Painted] scene that knows what its last frame changed.
// It is the browser's form of toolkit's scene.HostRoot.RenderDamaged
// (RenderDamaged(p, th) []toolkit.Rect): PaintDamaged paints into p only what
// changed since the previous call, confined to the rectangles it returns, in
// canvas pixels, and returns them. An EMPTY result means nothing changed, and
// the presenter then neither records nor presents the frame, and a non-empty
// one is recorded as PaintDamaged painted it: only the damage, over the
// renderer's persistent target, so PaintDamaged must repaint the ground under
// the damage as RenderDamaged does. RenderDamaged
// itself does not fit as is: it takes a *toolkit.Theme, which webcanvas does
// not own, so a scene wraps it (HostRoot and its theme) in this method.
//
// The presenter calls PaintDamaged on every frame, the first and the one after
// a resize included (the result is then ignored: those frames are whole), so
// that the scene's pending damage is drained as RenderDamaged drains it.
type DamagePainted interface {
	Painted
	PaintDamaged(p painter.Painter) []painter.Rect
}

// sink is where a frame goes: the renderer (draw the list over the target,
// present the target on the canvas), or a software target in tests.
type sink interface {
	Draw(l *gpu2d.List) error
	Present() error
}

// frames is the presenter's per-frame decision, kept free of the browser so
// that native tests check it pixel for pixel against a full repaint.
type frames struct {
	list  *gpu2d.List
	paint *gpupainter.Painter
	// whole forces the next frame to be a whole one: no frame yet, or a resize.
	whole bool
}

// use records into l from now on; the next frame is a whole one.
func (f *frames) use(l *gpu2d.List) {
	f.list, f.whole = l, true
	if f.paint == nil {
		f.paint = gpupainter.New(l)
	} else {
		f.paint.Reset(l)
	}
}

// rewind empties the list for a new recording.
func (f *frames) rewind() {
	f.list.Reset()
	f.paint.Reset(f.list)
}

// frame shows app on s and reports whether it did: a [DamagePainted] scene
// with no damage on a frame that is not a whole one is neither recorded nor
// presented.
func (f *frames) frame(app Painted, s sink) (bool, error) {
	f.rewind()
	dp, reports := app.(DamagePainted)
	whole := f.whole || !reports
	if reports {
		damaged := len(dp.PaintDamaged(f.paint)) > 0
		if !damaged && !f.whole {
			return false, nil
		}
	}
	// The recording of a damaged frame is PaintDamaged's own, confined to the
	// damage; the renderer composites it over its persistent target, which
	// holds the rest. Any other frame is recorded whole.
	if whole {
		f.rewind()
		app.Paint(f.paint)
	}
	f.whole = false
	f.paint.Flush()
	err := f.list.Err()
	if err == nil {
		err = s.Draw(f.list)
	}
	if err == nil {
		err = s.Present()
	}
	return true, err
}

// ErrNotPainted reports an App the presenter cannot record: it does not
// implement Painted.
var ErrNotPainted = errors.New("webcanvas/gpu: the App does not implement Painted")
