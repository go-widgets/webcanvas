// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package gpu

import (
	"bytes"
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"

	wg "github.com/go-gfx/webgpu"
	"github.com/go-widgets/painter"
)

// app paints a ground, rectangles, a translucent overlay and text: what a
// widget frame is made of.
type app struct{ w, h int }

func (a *app) Size() (int, int)    { return a.w, a.h }
func (a *app) Draw(buf []byte)     { a.Paint(painter.NewPixelPainter(buf, a.w, a.h)) }
func (*app) Click(int, int) bool   { return false }
func (*app) Move(int, int) bool    { return false }
func (*app) Release(int, int) bool { return false }
func (*app) Context(int, int) bool { return false }
func (*app) Char(string) bool      { return false }
func (*app) KeyDown(string) bool   { return false }
func (a *app) Paint(p painter.Painter) {
	p.FillRect(painter.Rect{W: a.w, H: a.h}, painter.RGBA{R: 30, G: 34, B: 40, A: 255})
	for i := range 12 {
		p.FillRect(painter.Rect{X: 4 + i*9, Y: 6 + i*3, W: 20, H: 11}, painter.RGBA{R: uint8(i * 20), G: 140, B: 200, A: 255})
	}
	p.FillRect(painter.Rect{X: 10, Y: 10, W: 60, H: 30}, painter.RGBA{R: 255, G: 255, B: 255, A: 90})
	p.Text(8, 50, "GPU canvas 0123", painter.RGBA{R: 240, G: 220, B: 90, A: 255})
}

// notPainted is an App without Paint: its own Paint, of another signature,
// hides the embedded one.
type notPainted struct{ *app }

func (notPainted) Paint() {}

func offscreen(w, h int) js.Value { return js.Global().Get("OffscreenCanvas").New(w, h) }

// The presenter draws what Draw paints on the CPU, byte for byte, and still
// does after the canvas is resized. (What reaches the canvas texture is the
// renderer's frame byte for byte: go-gfx/gpu2d's TestAPresentedFrameIsTheFrame.)
func TestThePresenterDrawsWhatDrawPaints(t *testing.T) {
	a := &app{w: 128, h: 72}
	canvas := offscreen(a.w, a.h)
	p := New()
	if !p.Attach(canvas, a, a.w, a.h) {
		if errors.Is(p.Err, wg.ErrNoCanvasContext) || p.Err != nil && p.dev == nil {
			t.Skipf("no WebGPU here: %v", p.Err)
		}
		t.Fatalf("Attach declined: %v", p.Err)
	}
	for _, size := range [][2]int{{128, 72}, {160, 90}} {
		a.w, a.h = size[0], size[1]
		canvas.Set("width", a.w)
		canvas.Set("height", a.h)
		p.Frame(a, a.w, a.h)
		if p.Err != nil {
			t.Fatalf("%d × %d: %v", a.w, a.h, p.Err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := p.r.Read(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		want := make([]byte, 4*a.w*a.h)
		a.Draw(want)
		if !bytes.Equal(got, want) {
			for i := range want {
				if got[i] != want[i] {
					px := i / 4
					t.Fatalf("%d × %d: first difference at (%d,%d) channel %d: CPU %d, GPU %d", a.w, a.h, px%a.w, px/a.w, i%4, want[i], got[i])
				}
			}
		}
	}
}

// An App that cannot paint through a Painter is declined, and the canvas is
// left untouched: a 2D context can still be had from it.
func TestAnAppWithoutPaintIsDeclined(t *testing.T) {
	canvas := offscreen(8, 8)
	p := New()
	if p.Attach(canvas, notPainted{&app{w: 8, h: 8}}, 8, 8) {
		t.Fatal("Attach took an App that is not Painted")
	}
	if !errors.Is(p.Err, ErrNotPainted) {
		t.Fatalf("Err = %v, want ErrNotPainted", p.Err)
	}
	if ctx := canvas.Call("getContext", "2d"); ctx.IsNull() {
		t.Fatal("the declined canvas no longer gives a 2D context")
	}
}
