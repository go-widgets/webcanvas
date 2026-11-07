// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package gpu

import (
	"context"
	"syscall/js"
	"time"

	"github.com/go-gfx/gpu2d"
	gpu2dwebgpu "github.com/go-gfx/gpu2d/webgpu"
	wg "github.com/go-gfx/webgpu"
	"github.com/go-widgets/gpupainter"
	"github.com/go-widgets/webcanvas"
)

// setupTimeout bounds the adapter and device requests: a browser that never
// answers falls back rather than leaving the page blank.
const setupTimeout = 10 * time.Second

// Presenter draws a Painted App on the GPU. Use one per canvas.
type Presenter struct {
	// Err is why the presenter declined the canvas, or why the last frame
	// failed; nil while it draws.
	Err error

	dev     *wg.Device
	r       *gpu2dwebgpu.Renderer
	surface *wg.Surface
	list    *gpu2d.List
	paint   *gpupainter.Painter
	w, h    int
}

var _ webcanvas.Presenter = (*Presenter)(nil)

// New returns a presenter; webcanvas.RunWith attaches it.
func New() *Presenter { return &Presenter{} }

// Attach takes the canvas if the App is Painted and WebGPU gives a device,
// a renderer and a surface. It asks the canvas for its WebGPU context last,
// so every earlier failure leaves the canvas to webcanvas' 2D path.
func (p *Presenter) Attach(canvas js.Value, app webcanvas.App, w, h int) bool {
	if _, ok := app.(Painted); !ok {
		p.Err = ErrNotPainted
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	nav, err := wg.Navigator()
	if err != nil {
		p.Err = err
		return false
	}
	a, err := nav.RequestAdapter(ctx, nil)
	if err != nil {
		p.Err = err
		return false
	}
	dev, err := a.RequestDevice(ctx, &wg.DeviceDescriptor{Label: "webcanvas/gpu"})
	if err != nil {
		p.Err = err
		return false
	}
	if err := wg.InstallReplay(ctx, ""); err != nil {
		dev.Destroy()
		p.Err = err
		return false
	}
	if p.Err = p.build(ctx, dev, w, h); p.Err != nil {
		dev.Destroy()
		return false
	}
	s, err := dev.NewSurfaceFromCanvas(canvas, gpu2dwebgpu.CanvasDescriptor())
	if err != nil {
		dev.Destroy()
		p.Err = err
		return false
	}
	p.dev, p.surface = dev, s
	return true
}

// build makes the renderer, list and painter for a w × h frame.
func (p *Presenter) build(ctx context.Context, dev *wg.Device, w, h int) error {
	r, err := gpu2dwebgpu.New(ctx, dev, w, h)
	if err != nil {
		return err
	}
	l, err := gpu2d.NewList(w, h)
	if err != nil {
		r.Destroy()
		return err
	}
	if p.r != nil {
		p.r.Destroy() // its memory goes back to the device's quota
	}
	p.r, p.list, p.w, p.h = r, l, w, h
	if p.paint == nil {
		p.paint = gpupainter.New(l)
	} else {
		p.paint.Reset(l)
	}
	return nil
}

// Frame records the App, draws it and presents it. A new canvas size (a
// webcanvas.Resizer scene) rebuilds the renderer at that size first.
func (p *Presenter) Frame(app webcanvas.App, w, h int) {
	if w != p.w || h != p.h {
		ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
		err := p.build(ctx, p.dev, w, h)
		cancel()
		if err == nil {
			err = p.surface.Resize(w, h)
		}
		if err != nil {
			p.Err = err
			return
		}
	}
	p.list.Reset()
	p.paint.Reset(p.list)
	app.(Painted).Paint(p.paint)
	p.paint.Flush()
	err := p.list.Err()
	if err == nil {
		err = p.r.Draw(p.list, nil)
	}
	if err == nil {
		err = p.r.Present(p.surface)
	}
	p.Err = err
}
