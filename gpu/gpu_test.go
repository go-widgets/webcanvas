// SPDX-License-Identifier: BSD-3-Clause

package gpu

import (
	"bytes"
	"errors"
	"testing"

	"github.com/go-gfx/gpu2d"
	"github.com/go-widgets/painter"
)

const tw, th = 48, 32

var (
	blue = painter.RGBA{B: 255, A: 255}
	cols = [...]painter.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {R: 255, G: 255, A: 255}}
	box  = [...]painter.Rect{{X: 4, Y: 4, W: 10, H: 8}, {X: 30, Y: 18, W: 12, H: 10}}
)

// scene is a Painted scene of a blue ground and two boxes whose colours change.
// With report set it is also a DamagePainted one: SetBox queues the box's
// rectangle, and PaintDamaged paints the queue (clipped to it) and returns it.
type scene struct {
	colour  [2]int
	pending []painter.Rect
	painted int // calls to Paint
	probes  int // calls to PaintDamaged
}

func (*scene) Size() (int, int)    { return tw, th }
func (*scene) Draw([]byte)         {}
func (*scene) Click(int, int) bool { return false }
func (*scene) Move(int, int) bool  { return false }
func (*scene) Release(int, int) bool {
	return false
}
func (*scene) Context(int, int) bool { return false }
func (*scene) Char(string) bool      { return false }
func (*scene) KeyDown(string) bool   { return false }

func (s *scene) SetBox(i, c int) {
	s.colour[i] = c
	s.pending = append(s.pending, box[i])
}

func (s *scene) draw(p painter.Painter, r painter.Rect) {
	c := p.(painter.Clipper)
	c.PushClip(r)
	p.FillRect(painter.Rect{W: tw, H: th}, blue)
	for i, b := range box {
		p.FillRect(b, cols[s.colour[i]])
	}
	c.PopClip()
}

func (s *scene) Paint(p painter.Painter) {
	s.painted++
	s.draw(p, painter.Rect{W: tw, H: th})
}

// plain is the scene without damage reporting: its method set has no PaintDamaged.
type plain struct{ *scene }

// reporting is the same scene with it.
type reporting struct{ *scene }

func (s reporting) PaintDamaged(p painter.Painter) []painter.Rect {
	s.probes++
	out := s.pending
	s.pending = nil
	for _, r := range out {
		s.draw(p, r)
	}
	return out
}

// target is a software canvas: the renderer's persistent target (Draw composites
// over its content) and the canvas the target is presented on.
type target struct {
	frame, canvas []byte
	draws, shown  int
	instances     []int // per Draw: what the renderer was handed
	fail          error
	failAt        string
}

func newTarget() *target {
	return &target{frame: make([]byte, 4*tw*th), canvas: make([]byte, 4*tw*th)}
}

func (t *target) Draw(l *gpu2d.List) error {
	if t.failAt == "draw" {
		return t.fail
	}
	t.draws++
	t.instances = append(t.instances, len(l.Instances()))
	return l.Rasterize(t.frame)
}

func (t *target) Present() error {
	if t.failAt == "present" {
		return t.fail
	}
	t.shown++
	copy(t.canvas, t.frame)
	return nil
}

func newFrames(t *testing.T) *frames {
	t.Helper()
	l, err := gpu2d.NewList(tw, th)
	if err != nil {
		t.Fatal(err)
	}
	f := &frames{}
	f.use(l)
	return f
}

// full is what a full repaint of s shows.
func full(t *testing.T, s *scene) []byte {
	t.Helper()
	f, tg := newFrames(t), newTarget()
	cp := *s // Paint counts calls: leave the scene's own count alone
	if _, err := f.frame(plain{&cp}, tg); err != nil {
		t.Fatal(err)
	}
	return tg.canvas
}

// A scene with no damage costs nothing: no recording the sink sees, no draw,
// no present. Every other frame is shown, and the canvas always equals a full
// repaint.
func TestAFrameWithNoDamageIsSkipped(t *testing.T) {
	s := &scene{}
	app := reporting{s}
	f, tg := newFrames(t), newTarget()
	steps := []struct {
		name  string
		act   func()
		shown bool
	}{
		{"first frame is whole", func() {}, true},
		{"idle", func() {}, false},
		{"one box recoloured", func() { s.SetBox(0, 1) }, true},
		{"idle again", func() {}, false},
		{"both boxes", func() { s.SetBox(0, 2); s.SetBox(1, 1) }, true},
		{"idle at the end", func() {}, false},
	}
	draws, shown := 0, 0
	for _, st := range steps {
		st.act()
		did, err := f.frame(app, tg)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if did != st.shown {
			t.Fatalf("%s: shown = %v, want %v", st.name, did, st.shown)
		}
		if st.shown {
			draws++
			shown++
		}
		if tg.draws != draws || tg.shown != shown {
			t.Fatalf("%s: %d draws and %d presents, want %d and %d", st.name, tg.draws, tg.shown, draws, shown)
		}
		if !bytes.Equal(tg.canvas, full(t, s)) {
			t.Fatalf("%s: the canvas differs from a full repaint", st.name)
		}
	}
	if s.probes != len(steps) {
		t.Fatalf("PaintDamaged called %d times, want once a frame (%d)", s.probes, len(steps))
	}
	// Only the first frame is recorded whole: a damaged frame is PaintDamaged's
	// own recording, which holds fewer instances and never calls Paint.
	if s.painted != 1 || len(tg.instances) != 3 || tg.instances[1] >= tg.instances[0] {
		t.Fatalf("Paint called %d times; instances per drawn frame %v; want one whole frame, then a one-box frame smaller than it", s.painted, tg.instances)
	}
}

// The first frame and the frame after a resize are whole even when the scene
// reports nothing, and the probe still drains the scene's damage.
func TestAWholeFrameIsShownWithoutDamage(t *testing.T) {
	s := &scene{}
	app := reporting{s}
	f, tg := newFrames(t), newTarget()
	for range 2 {
		did, err := f.frame(app, tg)
		if err != nil || !did {
			t.Fatalf("whole frame: shown %v, err %v", did, err)
		}
		if did, _ = f.frame(app, tg); did {
			t.Fatal("the frame after a whole one, with no damage, was shown")
		}
		l, err := gpu2d.NewList(tw, th) // a resize hands the frames a new list
		if err != nil {
			t.Fatal(err)
		}
		f.use(l)
	}
	s.SetBox(1, 2)
	f.use(f.list)
	if did, _ := f.frame(app, tg); !did || s.probes != 5 || len(s.pending) != 0 {
		t.Fatalf("shown %v, probes %d, pending %v", did, s.probes, s.pending)
	}
}

// A scene that does not report damage is recorded and presented every frame,
// as before.
func TestAPlainSceneIsShownEveryFrame(t *testing.T) {
	s := &scene{}
	f, tg := newFrames(t), newTarget()
	for i := range 3 {
		did, err := f.frame(plain{s}, tg)
		if err != nil || !did {
			t.Fatalf("frame %d: shown %v, err %v", i, did, err)
		}
	}
	if s.painted != 3 || tg.draws != 3 || tg.shown != 3 {
		t.Fatalf("Paint %d, draws %d, presents %d; want 3 each", s.painted, tg.draws, tg.shown)
	}
	if !bytes.Equal(tg.canvas, full(t, s)) {
		t.Fatal("the canvas differs from a full repaint")
	}
}

// A failing draw or present is reported, and a failed list is not drawn.
func TestAFailingFrameIsReported(t *testing.T) {
	boom := errors.New("boom")
	for _, at := range []string{"draw", "present"} {
		f, tg := newFrames(t), newTarget()
		tg.fail, tg.failAt = boom, at
		if did, err := f.frame(plain{&scene{}}, tg); !did || !errors.Is(err, boom) {
			t.Fatalf("%s: shown %v, err %v", at, did, err)
		}
	}
	f, tg := newFrames(t), newTarget()
	did, err := f.frame(refused{&scene{}}, tg)
	if !did || !errors.Is(err, gpu2d.ErrTooLarge) || tg.draws != 0 || tg.shown != 0 {
		t.Fatalf("a refused list: shown %v, err %v, draws %d, presents %d", did, err, tg.draws, tg.shown)
	}
}

// refused paints a rounded rectangle the list refuses.
type refused struct{ *scene }

func (refused) Paint(p painter.Painter) {
	p.FillRoundRect(painter.Rect{W: 1 << 16, H: 1 << 16}, 1<<15, blue)
}
