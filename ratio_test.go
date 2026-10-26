package webcanvas

import (
	"math"
	"testing"
)

func TestPixelRatio(t *testing.T) {
	for in, want := range map[float64]float64{1: 1, 2: 2, 1.5: 1.5, 0.25: 0.25, 8: 8, 0: 1, -2: 1, 0.1: 1, 9: 1, math.NaN(): 1, math.Inf(1): 1} {
		if got := normalRatio(in); got != want {
			t.Errorf("normalRatio(%v) = %v, want %v", in, got, want)
		}
	}
	for _, c := range []struct {
		css   int
		ratio float64
		want  int
	}{{1280, 1, 1280}, {1280, 2, 2560}, {641, 1.5, 961}, {1, 0.25, 1}} {
		if got := devicePixels(c.css, c.ratio); got != c.want {
			t.Errorf("devicePixels(%d, %v) = %d, want %d", c.css, c.ratio, got, c.want)
		}
	}
	if q := resolutionQuery(2); q != "(resolution: 2dppx)" {
		t.Errorf("query = %q", q)
	}
	if q := resolutionQuery(1.25); q != "(resolution: 1.25dppx)" {
		t.Errorf("query = %q", q)
	}
}
