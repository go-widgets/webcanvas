// Copyright (c) 2026 the go-widgets/gallery authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package webcanvas

import "testing"

// scrollApp is a minimal [Scroller] (and [App]) that records the last scroll it
// received, so a test can assert the harness would route a wheel to it.
type scrollApp struct {
	flakyApp
	lastX, lastY, lastDX, lastDY int
	scrolls                      int
}

func (a *scrollApp) Scroll(x, y, dx, dy int) bool {
	a.lastX, a.lastY, a.lastDX, a.lastDY = x, y, dx, dy
	a.scrolls++
	return dx != 0 || dy != 0
}

// A scrollApp is both an App and a Scroller — exactly what the wheel wiring keys
// off with a type assertion.
var (
	_ App      = (*scrollApp)(nil)
	_ Scroller = (*scrollApp)(nil)
)

// TestScrollRowsPerMode pins the WheelEvent-delta → toolkit-rows conversion across
// every deltaMode, both signs, the sub-row rounding floor and the zero case.
func TestScrollRowsPerMode(t *testing.T) {
	cases := []struct {
		name  string
		delta float64
		mode  int
		want  int
	}{
		{"zero is no rows", 0, 0, 0},
		{"pixel notch down rounds to rows", 100, 0, 2}, // 100/40 = 2.5 -> 2
		{"pixel notch up rounds to rows", -100, 0, -2}, // -2.5 -> -2
		{"tiny pixel down floors to one row", 8, 0, 1}, // 0.2 -> +1
		{"tiny pixel up floors to one row", -8, 0, -1}, // -0.2 -> -1
		{"line mode passes through", 3, deltaModeLine, 3},
		{"line mode negative", -2, deltaModeLine, -2},
		{"sub-line floors to one row", 0.4, deltaModeLine, 1},
		{"page mode multiplies out", 1, deltaModePage, wheelPageRows},
		{"page mode negative", -2, deltaModePage, -2 * wheelPageRows},
		{"unknown mode treated as pixels", 80, 7, 2}, // 80/40 = 2
	}
	for _, c := range cases {
		if got := scrollRows(c.delta, c.mode); got != c.want {
			t.Errorf("%s: scrollRows(%v, %d) = %d, want %d", c.name, c.delta, c.mode, got, c.want)
		}
	}
}

// TestScrollerContract exercises a Scroller through the same call shape the wheel
// listener uses (rows already converted), proving the interface an app opts into
// is satisfiable and reports repaint on a real delta.
func TestScrollerContract(t *testing.T) {
	var app App = &scrollApp{}
	sc, ok := app.(Scroller)
	if !ok {
		t.Fatal("scrollApp does not satisfy Scroller")
	}
	if !sc.Scroll(10, 20, 0, scrollRows(120, 0)) {
		t.Fatal("a nonzero scroll should request a repaint")
	}
	if sc.Scroll(10, 20, 0, 0) {
		t.Fatal("a zero scroll should not request a repaint")
	}
	got := app.(*scrollApp)
	if got.lastX != 10 || got.lastY != 20 || got.lastDY != 0 || got.scrolls != 2 {
		t.Fatalf("Scroll recorded (x=%d,y=%d,dy=%d,n=%d), want (10,20,0,2)", got.lastX, got.lastY, got.lastDY, got.scrolls)
	}
}
