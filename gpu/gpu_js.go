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
	f       frames
	w, h    int
	// Presented counts the frames that reached the canvas; Skipped, those a
	// DamagePainted scene reported no damage for.
	Presented, Skipped int
}

// renderer is the sink a frame goes to: the renderer, then the surface.
type renderer struct{ p *Presenter }

func (s renderer) Draw(l *gpu2d.List) error { return s.p.r.Draw(l, nil) }
func (s renderer) Present() error           { return s.p.r.Present(s.p.surface) }

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
	p.r, p.w, p.h = r, w, h
	p.f.use(l)
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
	shown, err := p.f.frame(app.(Painted), renderer{p})
	if shown {
		p.Presented++
	} else {
		p.Skipped++
	}
	p.Err = err
}
