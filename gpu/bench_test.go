// SPDX-License-Identifier: BSD-3-Clause

package gpu

import (
	"testing"

	"github.com/go-gfx/gpu2d"
	"github.com/go-widgets/painter"
)

// grid is a mostly-static widget scene: 40 × 30 cells of 24 × 24 pixels, each a
// filled rectangle and a label, on a 1024 × 768 canvas. It reports no damage.
type grid struct{ *scene }

func (grid) Paint(p painter.Painter) {
	p.FillRect(painter.Rect{W: 1024, H: 768}, blue)
	for y := range 30 {
		for x := range 40 {
			r := painter.Rect{X: x * 25, Y: y * 25, W: 24, H: 24}
			p.FillRect(r, cols[(x+y)%3])
			p.Text(r.X+2, r.Y+8, "ab", blue)
		}
	}
}

// gridReporting is the same scene, reporting what changed (nothing).
type gridReporting struct{ grid }

func (gridReporting) PaintDamaged(painter.Painter) []painter.Rect { return nil }

// gridOneCell reports one damaged cell per frame and paints only it.
type gridOneCell struct{ grid }

func (gridOneCell) PaintDamaged(p painter.Painter) []painter.Rect {
	r := painter.Rect{X: 500, Y: 250, W: 24, H: 24}
	c := p.(painter.Clipper)
	c.PushClip(r)
	p.FillRect(r, cols[1])
	p.Text(r.X+2, r.Y+8, "ab", blue)
	c.PopClip()
	return []painter.Rect{r}
}

// BenchmarkDamagedFrame is the cost of recording a frame in which one cell of
// 1,200 changed: the scene without damage reporting records them all, one
// that reports its cell records that cell only. Instances is what the
// renderer would be handed.
func BenchmarkDamagedFrame(b *testing.B) {
	for _, c := range []struct {
		name string
		app  Painted
	}{{"Whole", grid{&scene{}}}, {"Damage", gridOneCell{grid{&scene{}}}}} {
		b.Run(c.name, func(b *testing.B) {
			l, err := gpu2d.NewList(1024, 768)
			if err != nil {
				b.Fatal(err)
			}
			f := &frames{}
			f.use(l)
			if _, err := f.frame(c.app, nothing{}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := f.frame(c.app, nothing{}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(l.Instances())), "instances")
		})
	}
}

// nothing is a sink that discards: the cost measured is the presenter's
// recording and decision, which is what a skipped frame saves besides the
// renderer's two submits.
type nothing struct{}

func (nothing) Draw(*gpu2d.List) error { return nil }
func (nothing) Present() error         { return nil }

// BenchmarkIdleFrame is the cost of a frame in which nothing changed: a scene
// without damage reporting is recorded again whole, one that reports none is
// not recorded at all.
func BenchmarkIdleFrame(b *testing.B) {
	for _, c := range []struct {
		name string
		app  Painted
	}{{"Whole", grid{&scene{}}}, {"Skipped", gridReporting{grid{&scene{}}}}} {
		b.Run(c.name, func(b *testing.B) {
			l, err := gpu2d.NewList(1024, 768)
			if err != nil {
				b.Fatal(err)
			}
			f := &frames{}
			f.use(l)
			if _, err := f.frame(c.app, nothing{}); err != nil { // the whole first frame
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := f.frame(c.app, nothing{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
