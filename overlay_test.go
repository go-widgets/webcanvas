// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math"
	"strings"
	"testing"
)

// holes is the hole subpaths of a clip path: everything after the outer ring.
func holes(t *testing.T, path string) []string {
	t.Helper()
	if path == "" {
		return nil
	}
	const head = `path(evenodd, "M0 0H100000V100000H0Z`
	if !strings.HasPrefix(path, head) {
		t.Fatalf("the path does not start with the outer ring: %q", path)
	}
	if !strings.HasSuffix(path, `")`) {
		t.Fatalf("the path is not closed: %q", path)
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(path, head), `")`)
	if rest == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(strings.TrimPrefix(rest, " "), " M") {
		out = append(out, strings.TrimPrefix(p, "M"))
	}
	return out
}

// ⛔ NOTHING TO PUNCH IS NOT AN EMPTY PATH. The caller applies "" as
// clip-path: none; an empty path() clips the whole element away, so returning
// one would hide the content this exists to keep visible.
func TestNoOverlaysPunchNothing(t *testing.T) {
	for _, c := range []struct {
		what string
		in   []Overlay
	}{
		{"none at all", nil},
		{"an empty list", []Overlay{}},
		{"a window with no width", []Overlay{{X: 10, Y: 10, W: 0, H: 50}}},
		{"a window with no height", []Overlay{{X: 10, Y: 10, W: 50, H: 0}}},
		{"a negative rectangle", []Overlay{{X: 10, Y: 10, W: -50, H: -50}}},
	} {
		if got := ClipPath(c.in, 1); got != "" {
			t.Errorf("%s gave %q, want the empty string", c.what, got)
		}
	}
}

// ⛔⛔ ONE CLOSED SUBPATH PER HOLE, filled evenodd. A single polygon threading
// from the outer ring in to each hole and back has that journey drawn, and it
// leaves a wedge cut out of the content. This is the shape of the path, which
// is the part that was got wrong.
func TestEachHoleIsItsOwnClosedSubpath(t *testing.T) {
	got := ClipPath([]Overlay{
		{X: 10, Y: 20, W: 100, H: 50},
		{X: 200, Y: 300, W: 40, H: 60},
	}, 1)
	if !strings.HasPrefix(got, "path(evenodd,") {
		t.Errorf("the path is not filled evenodd: %q", got)
	}
	h := holes(t, got)
	if len(h) != 2 {
		t.Fatalf("%d subpath(s) for two windows: %q", len(h), got)
	}
	for i, p := range h {
		if !strings.HasSuffix(p, "Z") {
			t.Errorf("hole %d is not closed: %q", i, p)
		}
	}
}

// A square-cornered window punches a rectangle, at the right place.
func TestASquareWindowPunchesARectangle(t *testing.T) {
	h := holes(t, ClipPath([]Overlay{{X: 10, Y: 20, W: 100, H: 50}}, 1))
	if len(h) != 1 {
		t.Fatalf("%d holes", len(h))
	}
	if want := "10.0 20.0H110.0V70.0H10.0Z"; h[0] != want {
		t.Errorf("got %q, want %q", h[0], want)
	}
	if strings.Contains(h[0], "A") {
		t.Errorf("a square window drew an arc: %q", h[0])
	}
}

// ⛔ A ROUNDED WINDOW PUNCHES A ROUNDED HOLE. A square hole behind a rounded
// window cuts the content out at the corners while the canvas leaves them
// transparent, and the void between the two reads as black notches.
func TestARoundedWindowPunchesArcs(t *testing.T) {
	h := holes(t, ClipPath([]Overlay{{X: 0, Y: 0, W: 100, H: 100, Radius: 10}}, 1))
	if len(h) != 1 {
		t.Fatalf("%d holes", len(h))
	}
	if n := strings.Count(h[0], "A"); n != 4 {
		t.Errorf("%d arcs for four corners: %q", n, h[0])
	}
	// It starts after the top-left arc, not at the corner itself.
	if !strings.HasPrefix(h[0], "10.0 0.0") {
		t.Errorf("the path does not start after the top-left arc: %q", h[0])
	}
}

// ⛔ A radius larger than the window can carry is clamped rather than drawn:
// past half the shorter side the arcs cross and the hole stops being a shape.
func TestAnEnormousRadiusIsClamped(t *testing.T) {
	wide := holes(t, ClipPath([]Overlay{{W: 100, H: 40, Radius: 900}}, 1))[0]
	// Half of the SHORTER side is 20, so the top edge runs from 20 to 80.
	if !strings.HasPrefix(wide, "20.0 0.0H80.0") {
		t.Errorf("the radius was not clamped to half the shorter side: %q", wide)
	}
	// And a negative radius is square, not an arc the other way.
	sq := holes(t, ClipPath([]Overlay{{W: 100, H: 40, Radius: -5}}, 1))[0]
	if strings.Contains(sq, "A") {
		t.Errorf("a negative radius drew an arc: %q", sq)
	}
}

// The geometry is in DEVICE pixels and a path() reads CSS pixels, so the ratio
// divides. A retina scene that skipped this punched its holes at twice the
// size, in the wrong place.
func TestTheRatioConvertsDeviceToCSSPixels(t *testing.T) {
	h := holes(t, ClipPath([]Overlay{{X: 20, Y: 40, W: 200, H: 100}}, 2))[0]
	if want := "10.0 20.0H110.0V70.0H10.0Z"; h != want {
		t.Errorf("at a ratio of 2 the hole is %q, want %q", h, want)
	}
}

// ⛔ A ratio that is not a positive number is taken as 1 rather than producing
// NaN or Inf coordinates, which a browser rejects — and a rejected clip path
// is no clip path, so the window would be sliced off again.
func TestARatioThatIsNotANumberIsTakenAsOne(t *testing.T) {
	for _, dpr := range []float64{0, -2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		got := ClipPath([]Overlay{{X: 10, Y: 20, W: 100, H: 50}}, dpr)
		h := holes(t, got)
		if len(h) != 1 {
			t.Fatalf("dpr %v gave %d holes", dpr, len(h))
		}
		if want := "10.0 20.0H110.0V70.0H10.0Z"; h[0] != want {
			t.Errorf("dpr %v gave %q, want %q", dpr, h[0], want)
		}
	}
}

// ⛔ The holes are BOUNDED, as the layers are. A clip path grows with every
// hole and is rebuilt whenever a window moves.
func TestTheHolesAreBounded(t *testing.T) {
	in := make([]Overlay, MaxOverlays+20)
	for i := range in {
		in[i] = Overlay{X: i, Y: i, W: 10, H: 10}
	}
	if got := len(holes(t, ClipPath(in, 1))); got != MaxOverlays {
		t.Errorf("%d holes punched for %d windows, want the bound of %d",
			got, len(in), MaxOverlays)
	}
	// And the bound counts what is PUNCHED, not what was offered: empty
	// windows must not use up the budget.
	with := append([]Overlay{{W: 0, H: 0}, {W: 5, H: 0}}, in...)
	if got := len(holes(t, ClipPath(with, 1))); got != MaxOverlays {
		t.Errorf("%d holes when two empty windows came first, want %d", got, MaxOverlays)
	}
}
