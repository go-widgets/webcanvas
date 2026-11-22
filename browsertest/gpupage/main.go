// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for the GPU presenter's damage handling: a DamagePainted
// scene, 64x48, blue, with two boxes. window.__recolor(i, c) recolours box i
// (damage: its rectangle) and asks for a frame; window.__idle() asks for a frame
// with no damage. window.__stats() is [Presented, Skipped, Paint calls,
// PaintDamaged calls]; window.__err() is the presenter's Err, "" while it
// draws (a browser without WebGPU declines the canvas, and the test skips).
package main

import (
	"syscall/js"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/webcanvas"
	"github.com/go-widgets/webcanvas/gpu"
)

const w, h = 64, 48

var (
	blue   = painter.RGBA{B: 255, A: 255}
	colour = [...]painter.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {R: 255, G: 255, A: 255}}
	box    = [...]painter.Rect{{X: 4, Y: 4, W: 10, H: 8}, {X: 40, Y: 30, W: 16, H: 12}}
)

type scene struct {
	c       [2]int
	pending []painter.Rect
	request func()
	paints  int
	damaged int
}

func (*scene) Size() (int, int)      { return w, h }
func (*scene) Draw([]byte)           {}
func (*scene) Click(int, int) bool   { return false }
func (*scene) Move(int, int) bool    { return false }
func (*scene) Release(int, int) bool { return false }
func (*scene) Context(int, int) bool { return false }
func (*scene) Char(string) bool      { return false }
func (*scene) KeyDown(string) bool   { return false }

func (s *scene) RepaintWith(request func()) { s.request = request }

func (s *scene) draw(p painter.Painter, r painter.Rect) {
	c := p.(painter.Clipper)
	c.PushClip(r)
	p.FillRect(painter.Rect{W: w, H: h}, blue)
	for i, b := range box {
		p.FillRect(b, colour[s.c[i]])
	}
	c.PopClip()
}

func (s *scene) Paint(p painter.Painter) {
	s.paints++
	s.draw(p, painter.Rect{W: w, H: h})
}

func (s *scene) PaintDamaged(p painter.Painter) []painter.Rect {
	s.damaged++
	out := s.pending
	s.pending = nil
	for _, r := range out {
		s.draw(p, r)
	}
	return out
}

func main() {
	s := &scene{}
	pr := gpu.New()
	js.Global().Set("__recolor", js.FuncOf(func(_ js.Value, a []js.Value) any {
		i := a[0].Int()
		s.c[i] = a[1].Int()
		s.pending = append(s.pending, box[i])
		s.request()
		return nil
	}))
	js.Global().Set("__idle", js.FuncOf(func(js.Value, []js.Value) any {
		s.request()
		return nil
	}))
	js.Global().Set("__stats", js.FuncOf(func(js.Value, []js.Value) any {
		return []any{pr.Presented, pr.Skipped, s.paints, s.damaged}
	}))
	js.Global().Set("__err", js.FuncOf(func(js.Value, []js.Value) any {
		if pr.Err == nil {
			return ""
		}
		return pr.Err.Error()
	}))
	webcanvas.RunWith("screen", s, pr)
}
