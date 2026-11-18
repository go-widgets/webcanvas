// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for the incremental present: a DamageDrawer scene, 32x32,
// blue, with an 8x8 widget at (4,4) and an opaque 8x8 layer at (20,20). The page
// counts what reaches the 2D context (see damage_test.go). window.__recolor(n)
// repaints the widget in red n; window.__idle() asks for a frame in which only
// the layer's image is new.
package main

import (
	"syscall/js"

	"github.com/go-widgets/webcanvas"
)

var widget = webcanvas.Rect{X: 4, Y: 4, W: 8, H: 8}

type scene struct {
	red     byte
	pending []webcanvas.Rect
	layer   js.Value
	request func()
}

func (s *scene) Size() (int, int)      { return 32, 32 }
func (s *scene) Click(int, int) bool   { return false }
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Release(int, int) bool { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(string) bool      { return false }
func (s *scene) KeyDown(string) bool   { return false }

func (s *scene) RepaintWith(request func()) { s.request = request }

func (s *scene) paint(buf []byte, r webcanvas.Rect) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			p := buf[(y*32+x)*4:]
			if x >= widget.X && x < widget.X+widget.W && y >= widget.Y && y < widget.Y+widget.H {
				p[0], p[1], p[2], p[3] = s.red, 255, 0, 255
			} else {
				p[0], p[1], p[2], p[3] = 0, 0, 255, 255
			}
		}
	}
}

func (s *scene) Draw(buf []byte) {
	js.Global().Set("__draws", js.Global().Get("__draws").Int()+1)
	s.paint(buf, webcanvas.Rect{W: 32, H: 32})
}

func (s *scene) DrawDamaged(buf []byte) []webcanvas.Rect {
	js.Global().Set("__damaged", js.Global().Get("__damaged").Int()+1)
	out := s.pending
	s.pending = nil
	for _, r := range out {
		s.paint(buf, r)
	}
	return out
}

func (s *scene) Layers() []webcanvas.Layer {
	return []webcanvas.Layer{{Image: webcanvas.NewImage(s.layer), X: 20, Y: 20, W: 8, H: 8, Opaque: true}}
}

func main() {
	doc := js.Global().Get("document")
	img := doc.Call("createElement", "canvas")
	img.Set("width", 4)
	img.Set("height", 4)
	c := img.Call("getContext", "2d")
	c.Set("fillStyle", "#ff00ff")
	c.Call("fillRect", 0, 0, 4, 4)
	s := &scene{layer: img}
	js.Global().Set("__draws", 0)
	js.Global().Set("__damaged", 0)
	js.Global().Set("__recolor", js.FuncOf(func(_ js.Value, a []js.Value) any {
		s.red = byte(a[0].Int())
		s.pending = append(s.pending, widget)
		s.request()
		return nil
	}))
	js.Global().Set("__idle", js.FuncOf(func(js.Value, []js.Value) any {
		s.request()
		return nil
	}))
	webcanvas.RunWith("screen", s, nil)
}
