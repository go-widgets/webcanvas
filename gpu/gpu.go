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

	"github.com/go-widgets/painter"
	"github.com/go-widgets/webcanvas"
)

// Painted is an App that paints its whole frame, background included,
// through a painter.Painter, as it would into a PixelPainter in Draw.
type Painted interface {
	webcanvas.App
	Paint(p painter.Painter)
}

// ErrNotPainted reports an App the presenter cannot record: it does not
// implement Painted.
var ErrNotPainted = errors.New("webcanvas/gpu: the App does not implement Painted")
