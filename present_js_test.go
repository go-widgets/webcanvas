// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package webcanvas

import (
	"syscall/js"
	"testing"
)

// fakeContext is a 2D context that counts what a frame asks of it: putImageData
// calls (with the pixels they put, 4 bytes each, honouring the dirty rectangle)
// and drawImage calls. Run it with GOOS=js GOARCH=wasm under node.
func fakeContext() js.Value {
	return js.Global().Get("Function").New(`
		var c = {puts: 0, pixels: 0, draws: 0, last: null};
		c.createImageData = function (w, h) { return {width: w, height: h, data: new Uint8ClampedArray(w * h * 4)}; };
		c.putImageData = function (img, dx, dy, x, y, w, h) {
			c.puts++;
			c.last = [dx, dy, x, y, w, h];
			c.pixels += (w === undefined) ? img.width * img.height : w * h;
		};
		c.drawImage = function () { c.draws++; };
		return c;`).Invoke()
}

func newCanvasTarget(w, h int) (*canvasTarget, js.Value) {
	ctx := fakeContext()
	img := ctx.Call("createImageData", w, h)
	return &canvasTarget{ctx: ctx, imageData: img, dst: img.Get("data"), buf: make([]byte, 4*w*h), w: w, h: h}, ctx
}

func TestCanvasTargetPutsOnlyTheDirtyRect(t *testing.T) {
	const w, h = 64, 48
	for _, r := range []Rect{
		{X: 8, Y: 4, W: 40, H: 10}, // wide: one span
		{X: 60, Y: 2, W: 3, H: 30}, // narrow: row by row
		{X: 0, Y: 0, W: w, H: 1},
	} {
		ct, ctx := newCanvasTarget(w, h)
		for i := range ct.buf {
			ct.buf[i] = byte(i%251) + 1
		}
		ct.Put(r)
		if ctx.Get("puts").Int() != 1 || ctx.Get("pixels").Int() != r.W*r.H {
			t.Fatalf("%+v: puts %d, pixels %d; want 1 and %d", r, ctx.Get("puts").Int(), ctx.Get("pixels").Int(), r.W*r.H)
		}
		last := ctx.Get("last")
		if last.Index(2).Int() != r.X || last.Index(3).Int() != r.Y || last.Index(4).Int() != r.W || last.Index(5).Int() != r.H {
			t.Fatalf("%+v: dirty rect args %v", r, last)
		}
		// Every pixel inside r was copied; none outside the copied rows was.
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				got := ct.dst.Index((y*w + x) * 4).Int()
				inside := x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
				if inside && got != int(ct.buf[(y*w+x)*4]) {
					t.Fatalf("%+v: pixel (%d,%d) inside was not copied", r, x, y)
				}
				if !inside && (y < r.Y || y >= r.Y+r.H) && got != 0 {
					t.Fatalf("%+v: pixel (%d,%d) outside the rows was copied", r, x, y)
				}
			}
		}
	}
	ct, ctx := newCanvasTarget(w, h)
	ct.Put(Rect{W: w, H: h})
	if ctx.Get("puts").Int() != 1 || ctx.Get("pixels").Int() != w*h {
		t.Fatalf("whole put: puts %d, pixels %d", ctx.Get("puts").Int(), ctx.Get("pixels").Int())
	}
}

func TestCanvasTargetDrawLayerSkipsAForeignHandle(t *testing.T) {
	ct, ctx := newCanvasTarget(8, 8)
	ct.DrawLayer(Layer{Image: NewImage("not a js value"), W: 4, H: 4, Alpha: 1})
	if ctx.Get("draws").Int() != 0 {
		t.Fatal("drew a handle that is not a js.Value")
	}
	ct.DrawLayer(Layer{Image: NewImage(js.Global().Get("Object").New()), W: 4, H: 4, Alpha: 1})
	if ctx.Get("draws").Int() != 1 {
		t.Fatal("did not draw a js.Value handle")
	}
}

// benchScene is a 1280x800 damage-aware scene whose frame changes one 120x40
// widget, as a clock or a spinner does.
type benchScene struct {
	scene1280
	damage []Rect
}

type scene1280 struct{ n int }

func (s *scene1280) Size() (int, int)      { return 1280, 800 }
func (s *scene1280) Click(x, y int) bool   { return false }
func (s *scene1280) Move(x, y int) bool    { return false }
func (s *scene1280) Release(x, y int) bool { return false }
func (s *scene1280) Context(x, y int) bool { return false }
func (s *scene1280) Char(string) bool      { return false }
func (s *scene1280) KeyDown(string) bool   { return false }
func (s *scene1280) Draw(buf []byte) {
	s.n++
	fillRect(buf, Rect{W: 1280, H: 800}, byte(s.n))
}

// fillRect paints r with v in a 1280-wide RGBA buffer.
func fillRect(buf []byte, r Rect, v byte) {
	for y := r.Y; y < r.Y+r.H; y++ {
		row := buf[(y*1280+r.X)*4 : (y*1280+r.X+r.W)*4]
		for i := range row {
			row[i] = v
		}
	}
}

func (s *benchScene) DrawDamaged(buf []byte) []Rect {
	s.n++
	for _, r := range s.damage {
		fillRect(buf, r, byte(s.n))
	}
	return s.damage
}

// layerBench is benchScene with an opaque guest layer on top.
type layerBench struct{ benchScene }

func (l *layerBench) Layers() []Layer {
	return []Layer{{Image: NewImage(js.Global().Get("Object").New()), X: 200, Y: 100, W: 800, H: 600, Opaque: true}}
}

func benchFrames(b *testing.B, app App) {
	ct, _ := newCanvasTarget(1280, 800)
	c := newComposer()
	for i := 0; i < b.N; i++ {
		c.frame(app, ct.buf, 1280, 800, ct)
	}
}

// BenchmarkFrameWhole is a scene that reports no damage: Draw, copy and put the
// whole 1280x800 framebuffer every frame (what every frame cost before).
func BenchmarkFrameWhole(b *testing.B) { benchFrames(b, &scene1280{}) }

// BenchmarkFrameOneWidget repaints, copies and puts one 120x40 widget.
func BenchmarkFrameOneWidget(b *testing.B) {
	benchFrames(b, &benchScene{damage: []Rect{{X: 600, Y: 20, W: 120, H: 40}}})
}

// BenchmarkFrameLayerOnly is a frame with no scene damage and an opaque layer.
func BenchmarkFrameLayerOnly(b *testing.B) { benchFrames(b, &layerBench{}) }

// BenchmarkFrameLayerWholeScene is the same scene as BenchmarkFrameLayerOnly
// without the damage contract: the old behaviour of a guest frame.
type layerWhole struct{ scene1280 }

func (l *layerWhole) Layers() []Layer {
	return []Layer{{Image: NewImage(js.Global().Get("Object").New()), X: 200, Y: 100, W: 800, H: 600}}
}

func BenchmarkFrameLayerWholeScene(b *testing.B) { benchFrames(b, &layerWhole{}) }
