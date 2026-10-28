// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math"
	"testing"
)

type layered struct {
	plainApp
	layers []Layer
}

func (l *layered) Layers() []Layer { return l.layers }

func TestImage(t *testing.T) {
	if !(Image{}).IsZero() || NewImage(nil).Handle() != nil {
		t.Error("the zero Image")
	}
	im := NewImage("bitmap")
	if im.IsZero() || im.Handle() != "bitmap" {
		t.Errorf("NewImage = %+v", im)
	}
}

func TestDrawable(t *testing.T) {
	im := NewImage(1)
	for _, c := range []struct {
		l     Layer
		ok    bool
		alpha float64
	}{
		{Layer{Image: im, X: 0, Y: 0, W: 10, H: 10}, true, 1},
		{Layer{Image: im, X: -5, Y: -5, W: 10, H: 10, Alpha: 0.4}, true, 0.4},
		{Layer{Image: im, X: 90, Y: 90, W: 50, H: 50, Alpha: 1}, true, 1},
		{Layer{X: 0, Y: 0, W: 10, H: 10}, false, 0},
		{Layer{Image: im, W: 0, H: 10}, false, 0},
		{Layer{Image: im, W: 10, H: -1}, false, 0},
		{Layer{Image: im, X: 100, W: 10, H: 10}, false, 0},
		{Layer{Image: im, Y: 100, W: 10, H: 10}, false, 0},
		{Layer{Image: im, X: -10, W: 10, H: 10}, false, 0},
		{Layer{Image: im, Y: -10, W: 10, H: 10}, false, 0},
		{Layer{Image: im, W: 10, H: 10, Alpha: 1.5}, false, 0},
		{Layer{Image: im, W: 10, H: 10, Alpha: -0.1}, false, 0},
		{Layer{Image: im, W: 10, H: 10, Alpha: math.NaN()}, false, 0},
	} {
		got, ok := drawable(c.l, 100, 100)
		if ok != c.ok || ok && got.Alpha != c.alpha {
			t.Errorf("drawable(%+v) = %+v, %v", c.l, got, ok)
		}
	}
}

func TestLayersOf(t *testing.T) {
	if layersOf(plainApp{}, 10, 10) != nil {
		t.Error("a scene without layers has some")
	}
	var many []Layer
	for i := 0; i < MaxLayers+5; i++ {
		many = append(many, Layer{Image: NewImage(i), W: 1, H: 1})
	}
	many = append([]Layer{{W: 1, H: 1}}, many...) // not drawable, skipped
	got := layersOf(&layered{layers: many}, 10, 10)
	if len(got) != MaxLayers || got[0].Image.Handle() != 0 {
		t.Errorf("layersOf = %d layers, first %v", len(got), got[0].Image.Handle())
	}
}
