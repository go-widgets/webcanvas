// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func wheelAt(t *testing.T, ctx context.Context, x, y, dx, dy float64) {
	t.Helper()
	// DeltaX 0 is omitted from the JSON, and the protocol wants both.
	if dx == 0 {
		dx = 0.01
	}
	p := input.DispatchMouseEvent
	if _, err := chromedp.Call(ctx, p, input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseWheel, X: x, Y: y, DeltaX: dx, DeltaY: dy}); err != nil {
		t.Fatal(err)
	}
}

// The text layer lies over the canvas, so the pointer is over the layer: the
// scene must still be asked about the wheel, and the page scroll only when it
// says it did not use it.
func TestTheWheelOverTheTextLayerReachesTheScene(t *testing.T) {
	ctx := textPage(t, 1)
	setText(t, ctx, code)
	time.Sleep(400 * time.Millisecond)
	c := ev[[]float64](t, ctx, `(() => { const b = document.getElementById('screen').getBoundingClientRect(); return [b.left + b.width / 2, b.top + b.height / 2]; })()`)
	if !ev[bool](t, ctx, `!!document.elementFromPoint(`+ftoa(c[0])+`,`+ftoa(c[1])+`).closest('[data-webcanvas-layer]')`) {
		t.Fatal("the pointer is not over the text layer: this test would prove nothing")
	}
	wheels := func() int { return ev[int](t, ctx, `(window.__wheels || []).length`) }

	wheelAt(t, ctx, c[0], c[1], 0, 200)
	time.Sleep(300 * time.Millisecond)
	if wheels() == 0 {
		t.Fatal("the scene was never asked about a wheel over its text layer")
	}
	if y := ev[float64](t, ctx, `window.scrollY`); y <= 0 {
		t.Fatalf("the scene did not use the wheel and the page did not scroll (scrollY %v)", y)
	}

	// a scene that uses the wheel keeps it
	ev[any](t, ctx, `window.scrollTo(0, 0); setConsume(true)`)
	time.Sleep(200 * time.Millisecond)
	n := wheels()
	wheelAt(t, ctx, c[0], c[1], 0, 200)
	time.Sleep(300 * time.Millisecond)
	if wheels() != n+1 {
		t.Fatalf("wheels %d -> %d, want one more", n, wheels())
	}
	if y := ev[float64](t, ctx, `window.scrollY`); y != 0 {
		t.Fatalf("the scene used the wheel and the page scrolled anyway (scrollY %v)", y)
	}
	// a horizontal wheel arrives as dx
	wheelAt(t, ctx, c[0], c[1], 120, 0.01)
	time.Sleep(300 * time.Millisecond)
	last := ev[[]int](t, ctx, `window.__wheels[window.__wheels.length - 1]`)
	if last[0] <= 0 {
		t.Fatalf("a horizontal wheel reached the scene as %v", last)
	}
}

const resizeIndex = `<!doctype html><meta charset="utf-8">
<style>body{margin:0} canvas{display:block;width:200px;height:100px}</style>
<canvas id="a"></canvas><canvas id="b"></canvas>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// Surface.Resize lays out one surface at its canvas's CSS box and touches no
// other; after Close it does nothing.
func TestSurfaceResizeIsPerSurface(t *testing.T) {
	url := servePageHTML(t, "./resizepage", resizeIndex)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	until := func(js string) {
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](js)); err == nil && ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", js)
			}
		}
	}
	until(`window.resizeReady === true`)
	count := func(n string) int { return ev[int](t, ctx, `__resizeCount["`+n+`"]`) }
	a0, b0 := count("a"), count("b")
	ev[any](t, ctx, `document.getElementById('a').style.height = '160px'`)
	ev[any](t, ctx, `resizeSurface('a')`)
	if got := ev[[]int](t, ctx, `__resizes.a`); got[0] != 200 || got[1] != 160 {
		t.Fatalf("a was laid out at %v, want 200x160", got)
	}
	if a1, b1 := count("a"), count("b"); a1 != a0+1 || b1 != b0 {
		t.Fatalf("resize counts: a %d -> %d, b %d -> %d: want only a", a0, a1, b0, b1)
	}
	if h := ev[int](t, ctx, `document.getElementById('a').height`); h != 160 {
		t.Fatalf("a's backing store is %d high, want 160", h)
	}
	ev[any](t, ctx, `closeSurface('a')`)
	a1 := count("a")
	ev[any](t, ctx, `resizeSurface('a')`)
	if count("a") != a1 {
		t.Fatal("a closed surface was resized")
	}
}
