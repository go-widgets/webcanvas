// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"bytes"
	"testing"
)

const tw, th = 64, 48

// softImage is a layer's image in the tests: an RGBA bitmap.
type softImage struct {
	w, h int
	pix  []byte
}

func solid(w, h int, r, g, b, a byte) *softImage {
	im := &softImage{w: w, h: h, pix: make([]byte, 4*w*h)}
	for i := 0; i < w*h; i++ {
		copy(im.pix[4*i:], []byte{r, g, b, a})
	}
	return im
}

// softCanvas is a software 2D canvas: Put copies a rectangle of the scene
// buffer, DrawLayer blends a bitmap source-over, scaled nearest-neighbour.
type softCanvas struct {
	buf         []byte // the scene buffer Put reads
	pix         []byte
	puts        []Rect
	putPx       int
	layersDrawn int
}

func newSoft(buf []byte) *softCanvas { return &softCanvas{buf: buf, pix: make([]byte, 4*tw*th)} }

func (c *softCanvas) Put(r Rect) {
	c.puts = append(c.puts, r)
	c.putPx += r.W * r.H
	for y := r.Y; y < r.Y+r.H; y++ {
		copy(c.pix[(y*tw+r.X)*4:(y*tw+r.X+r.W)*4], c.buf[(y*tw+r.X)*4:(y*tw+r.X+r.W)*4])
	}
}

func (c *softCanvas) DrawLayer(l Layer) {
	im := l.Image.Handle().(*softImage)
	c.layersDrawn++
	r := layerRect(l, tw, th)
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			sx, sy := (x-l.X)*im.w/l.W, (y-l.Y)*im.h/l.H
			s := im.pix[(sy*im.w+sx)*4:]
			a := int(s[3]) * int(l.Alpha*256) / 256
			d := c.pix[(y*tw+x)*4:]
			for k := 0; k < 3; k++ {
				d[k] = byte((int(s[k])*a + int(d[k])*(255-a)) / 255)
			}
			d[3] = 255
		}
	}
}

func (c *softCanvas) reset() { c.puts, c.putPx, c.layersDrawn = nil, 0, 0 }

// scene is a damage-aware test scene: a gradient with one widget on it, and
// layers. Draw paints it whole; DrawDamaged paints only what changed since the
// last frame, into the persistent buffer.
type scene struct {
	widget  Rect
	colour  byte
	pending []Rect
	layers  []Layer
	draws   int
	damaged int
}

func (s *scene) Size() (int, int)      { return tw, th }
func (s *scene) Click(x, y int) bool   { return false }
func (s *scene) Move(x, y int) bool    { return false }
func (s *scene) Release(x, y int) bool { return false }
func (s *scene) Context(x, y int) bool { return false }
func (s *scene) Char(string) bool      { return false }
func (s *scene) KeyDown(string) bool   { return false }
func (s *scene) Layers() []Layer       { return s.layers }

func (s *scene) pixel(x, y int) [4]byte {
	if x >= s.widget.X && x < s.widget.X+s.widget.W && y >= s.widget.Y && y < s.widget.Y+s.widget.H {
		return [4]byte{s.colour, 200, 50, 255}
	}
	return [4]byte{byte(x * 4), byte(y * 5), 90, 255}
}

func (s *scene) paint(buf []byte, r Rect) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			p := s.pixel(x, y)
			copy(buf[(y*tw+x)*4:], p[:])
		}
	}
}

func (s *scene) Draw(buf []byte) {
	s.draws++
	s.pending = nil
	s.paint(buf, Rect{W: tw, H: th})
}

func (s *scene) DrawDamaged(buf []byte) []Rect {
	s.damaged++
	out := s.pending
	s.pending = nil
	for _, r := range out {
		s.paint(buf, clip(r, tw, th))
	}
	return out
}

// change edits the widget and records the damage: where it was and where it is.
func (s *scene) change(r Rect, colour byte) {
	s.pending = append(s.pending, s.widget, r)
	s.widget, s.colour = r, colour
}

var _ DamageDrawer = (*scene)(nil)

// reference is a full repaint of s: Draw, a whole put, every layer.
func reference(s *scene) []byte {
	buf := make([]byte, 4*tw*th)
	s.paint(buf, Rect{W: tw, H: th})
	c := newSoft(buf)
	c.Put(Rect{W: tw, H: th})
	for _, l := range layersOf(s, tw, th) {
		c.DrawLayer(l)
	}
	return c.pix
}

func TestLayerOnlyFramePutsNothing(t *testing.T) {
	s := &scene{widget: Rect{X: 4, Y: 4, W: 8, H: 8}}
	s.layers = []Layer{{Image: NewImage(solid(4, 4, 255, 0, 0, 255)), X: 20, Y: 20, W: 16, H: 16, Opaque: true}}
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	c.frame(s, buf, tw, th, cv) // the first frame is whole
	cv.reset()
	s.draws, s.damaged = 0, 0
	// The layer's image changed behind the same handle; the scene did not.
	s.layers[0].Image = NewImage(solid(4, 4, 0, 255, 0, 255))
	c.frame(s, buf, tw, th, cv)
	if s.draws != 0 || len(cv.puts) != 0 {
		t.Fatalf("a layer-only frame: Draw %d, puts %v; want none", s.draws, cv.puts)
	}
	if cv.layersDrawn != 1 {
		t.Fatalf("layers drawn = %d, want 1", cv.layersDrawn)
	}
	if !bytes.Equal(cv.pix, reference(s)) {
		t.Fatal("the canvas differs from a full repaint")
	}
}

func TestOneWidgetChangePutsOnlyItsRect(t *testing.T) {
	s := &scene{widget: Rect{X: 4, Y: 4, W: 8, H: 8}}
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	c.frame(s, buf, tw, th, cv)
	if len(cv.puts) != 1 || cv.puts[0] != (Rect{W: tw, H: th}) || s.draws != 1 {
		t.Fatalf("first frame: puts %v, Draw %d; want one whole put", cv.puts, s.draws)
	}
	cv.reset()
	s.change(Rect{X: 4, Y: 4, W: 8, H: 8}, 99)
	c.frame(s, buf, tw, th, cv)
	if s.draws != 1 || len(cv.puts) != 1 || cv.puts[0] != (Rect{X: 4, Y: 4, W: 8, H: 8}) || cv.putPx != 64 {
		t.Fatalf("one widget: Draw %d, puts %v (%d px); want its 8x8 rect", s.draws, cv.puts, cv.putPx)
	}
	if !bytes.Equal(cv.pix, reference(s)) {
		t.Fatal("the canvas differs from a full repaint")
	}
	// Damage reaching past the canvas is confined to it; wholly off it, dropped.
	cv.reset()
	s.pending = []Rect{{X: -5, Y: -5, W: 10, H: 10}, {X: 100, Y: 100, W: 5, H: 5}, {W: 0, H: 4}}
	c.frame(s, buf, tw, th, cv)
	if len(cv.puts) != 1 || cv.puts[0] != (Rect{W: 5, H: 5}) {
		t.Fatalf("confined damage: puts %v", cv.puts)
	}
}

func TestMovingAndTranslucentLayersEqualAFullRepaint(t *testing.T) {
	s := &scene{widget: Rect{X: 30, Y: 10, W: 10, H: 10}, colour: 10}
	red, blue := NewImage(solid(4, 4, 255, 0, 0, 255)), NewImage(solid(4, 4, 0, 0, 255, 255))
	ghost := NewImage(solid(4, 4, 255, 255, 255, 120)) // transparent pixels
	frames := []struct {
		name   string
		layers []Layer
		edit   func()
	}{
		{"no layer", nil, nil},
		{"opaque layer appears", []Layer{{Image: red, X: 5, Y: 5, W: 20, H: 20, Opaque: true}}, nil},
		{"layer moves", []Layer{{Image: red, X: 12, Y: 9, W: 20, H: 20, Opaque: true}}, nil},
		{"layer shrinks", []Layer{{Image: red, X: 12, Y: 9, W: 10, H: 10, Opaque: true}}, nil},
		{"layer fades", []Layer{{Image: red, X: 12, Y: 9, W: 10, H: 10, Opaque: true, Alpha: 0.5}}, nil},
		{"fading layer again", []Layer{{Image: red, X: 12, Y: 9, W: 10, H: 10, Opaque: true, Alpha: 0.5}}, nil},
		{"fading layer moves", []Layer{{Image: red, X: 14, Y: 11, W: 10, H: 10, Opaque: true, Alpha: 0.5}}, nil},
		{"two layers, one translucent image", []Layer{
			{Image: blue, X: 0, Y: 0, W: 30, H: 30, Opaque: true},
			{Image: ghost, X: 10, Y: 10, W: 30, H: 30},
		}, nil},
		{"the same, again", []Layer{
			{Image: blue, X: 0, Y: 0, W: 30, H: 30, Opaque: true},
			{Image: ghost, X: 10, Y: 10, W: 30, H: 30},
		}, nil},
		{"scene damage under and beside a layer", []Layer{
			{Image: blue, X: 0, Y: 0, W: 30, H: 30, Opaque: true},
			{Image: ghost, X: 10, Y: 10, W: 30, H: 30},
		}, func() { s.change(Rect{X: 20, Y: 20, W: 20, H: 20}, 200) }},
		{"layers go", nil, nil},
		{"layer off the canvas", []Layer{{Image: red, X: 60, Y: 40, W: 20, H: 20, Opaque: true}}, nil},
		{"scene change alone", nil, func() { s.change(Rect{X: 0, Y: 30, W: 20, H: 10}, 5) }},
	}
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	for i, f := range frames {
		s.layers = f.layers
		if f.edit != nil {
			f.edit()
		}
		cv.reset()
		c.frame(s, buf, tw, th, cv)
		if !bytes.Equal(cv.pix, reference(s)) {
			t.Fatalf("frame %d (%s): the canvas differs from a full repaint", i, f.name)
		}
		if i > 0 && s.draws != 1 {
			t.Fatalf("frame %d (%s): Draw ran %d times; only the first frame is whole", i, f.name, s.draws)
		}
	}
}

func TestStillOpaqueLayerPutsNothingButAFadedOneRestoresItsRect(t *testing.T) {
	s := &scene{}
	img := NewImage(solid(2, 2, 9, 9, 9, 255))
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	s.layers = []Layer{{Image: img, X: 8, Y: 8, W: 16, H: 16, Opaque: true}}
	c.frame(s, buf, tw, th, cv)
	cv.reset()
	c.frame(s, buf, tw, th, cv)
	if len(cv.puts) != 0 {
		t.Fatalf("a still opaque layer put %v", cv.puts)
	}
	s.layers[0].Opaque = false // an image that may be transparent
	cv.reset()
	c.frame(s, buf, tw, th, cv)
	if len(cv.puts) != 1 || cv.puts[0] != (Rect{X: 8, Y: 8, W: 16, H: 16}) {
		t.Fatalf("a layer that may be transparent: puts %v, want its own rect", cv.puts)
	}
}

func TestSceneWithoutDamageIsDrawnAndPutWhole(t *testing.T) {
	p := &plainScene{App: &scene{}}
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	for i := 0; i < 3; i++ {
		cv.reset()
		c.frame(p, buf, tw, th, cv)
		if len(cv.puts) != 1 || cv.puts[0] != (Rect{W: tw, H: th}) {
			t.Fatalf("frame %d: puts %v, want one whole put", i, cv.puts)
		}
	}
	if p.draws != 3 {
		t.Fatalf("Draw ran %d times, want 3", p.draws)
	}
}

// plainScene implements App only: embedding the interface hides DrawDamaged.
type plainScene struct {
	App
	draws int
}

func (p *plainScene) Draw(buf []byte) { p.draws++ }

func TestResetMakesTheNextFrameWhole(t *testing.T) {
	s := &scene{}
	buf := make([]byte, 4*tw*th)
	c, cv := newComposer(), newSoft(buf)
	c.frame(s, buf, tw, th, cv)
	c.reset()
	cv.reset()
	c.frame(s, buf, tw, th, cv)
	if s.draws != 2 || len(cv.puts) != 1 || cv.puts[0] != (Rect{W: tw, H: th}) {
		t.Fatalf("after reset: Draw %d, puts %v", s.draws, cv.puts)
	}
}

func TestRectHelpers(t *testing.T) {
	if got := clip(Rect{X: -2, Y: -2, W: 6, H: 6}, 10, 10); got != (Rect{W: 4, H: 4}) {
		t.Errorf("clip = %+v", got)
	}
	if got := clip(Rect{X: 20, W: 5, H: 5}, 10, 10); got != (Rect{}) {
		t.Errorf("clip off canvas = %+v", got)
	}
	if !contains(Rect{W: 10, H: 10}, Rect{X: 2, Y: 2, W: 3, H: 3}) || contains(Rect{W: 4, H: 4}, Rect{X: 2, Y: 2, W: 3, H: 3}) ||
		!contains(Rect{}, Rect{W: 0, H: 5}) {
		t.Error("contains")
	}
	got := coalesce([]Rect{{W: 10, H: 10}, {X: 1, Y: 1, W: 2, H: 2}, {W: 10, H: 10}, {X: 20, W: 4, H: 4}, {W: 0, H: 3}})
	if len(got) != 2 || got[0] != (Rect{W: 10, H: 10}) || got[1] != (Rect{X: 20, W: 4, H: 4}) {
		t.Errorf("coalesce = %+v", got)
	}
}
