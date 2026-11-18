// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// counted wraps the 2D context's putImageData and drawImage before the wasm
// starts, so the page can be asked what a frame sent to the canvas.
const counted = `<!doctype html><meta charset="utf-8">
<canvas id="screen"></canvas>
<script>
window.__puts = []; window.__draws2d = 0;
const put = CanvasRenderingContext2D.prototype.putImageData;
CanvasRenderingContext2D.prototype.putImageData = function (img, dx, dy, x, y, w, h) {
  window.__puts.push(w === undefined ? [0, 0, img.width, img.height] : [x, y, w, h]);
  return put.apply(this, arguments);
};
const draw = CanvasRenderingContext2D.prototype.drawImage;
CanvasRenderingContext2D.prototype.drawImage = function () { window.__draws2d++; return draw.apply(this, arguments); };
</script>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// A scene that reports damage is put on the canvas rectangle by rectangle: the
// first frame whole, a recoloured widget as its own rectangle, a frame whose
// only news is the layer's image as no putImageData at all -- and what the
// canvas shows is what a full repaint shows.
func TestDamageAwareSceneIsPutByRectangle(t *testing.T) {
	url := servePageHTML(t, "./damagepage", counted)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	ready(t, ctx)
	state := func() (puts [][]int, draws2d, sceneDraws, damaged int) {
		t.Helper()
		var err error
		if puts, err = chromedp.Run(ctx, chromedp.Evaluate[[][]int](`window.__puts`)); err != nil {
			t.Fatal(err)
		}
		n, err := chromedp.Run(ctx, chromedp.Evaluate[[]int](`[window.__draws2d, window.__draws, window.__damaged]`))
		if err != nil {
			t.Fatal(err)
		}
		return puts, n[0], n[1], n[2]
	}
	frame := func(js string) {
		t.Helper()
		if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](js)); err != nil {
			t.Fatal(err)
		}
		// The frame is an animation frame away; wait for it by its layer draw.
		before := mustInt(t, ctx, `window.__draws2d`)
		for deadline := time.Now().Add(5 * time.Second); mustInt(t, ctx, `window.__draws2d`) == before; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the frame was never drawn")
			}
		}
	}
	puts, _, draws, _ := state()
	if len(puts) != 1 || puts[0][2] != 32 || puts[0][3] != 32 || draws != 1 {
		t.Fatalf("first frame: puts %v, Draw %d; want one whole put", puts, draws)
	}

	frame(`window.__recolor(255)`)
	puts, _, draws, damaged := state()
	if len(puts) != 2 || puts[1][0] != 4 || puts[1][1] != 4 || puts[1][2] != 8 || puts[1][3] != 8 || draws != 1 || damaged != 1 {
		t.Fatalf("a recoloured widget: puts %v, Draw %d, DrawDamaged %d; want its 8x8 rect", puts, draws, damaged)
	}

	frame(`window.__idle()`)
	puts, _, _, _ = state()
	if len(puts) != 2 {
		t.Fatalf("a frame with no damage put %v", puts[2:])
	}

	// The canvas equals a full repaint: widget red+green, the rest blue, layer magenta.
	got, err := chromedp.Run(ctx, chromedp.Evaluate[[][]int](`(() => {
		const c = document.getElementById("screen").getContext("2d");
		return [[5,5],[3,3],[31,0],[22,22],[19,19]].map(([x,y]) => Array.from(c.getImageData(x,y,1,1).data));
	})()`))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{255, 255, 0, 255}, {0, 0, 255, 255}, {0, 0, 255, 255}, {255, 0, 255, 255}, {0, 0, 255, 255}}
	for i := range want {
		for k := range want[i] {
			if got[i][k] != want[i][k] {
				t.Fatalf("canvas pixels %v, want %v", got, want)
			}
		}
	}
}

func mustInt(t *testing.T, ctx context.Context, js string) int {
	t.Helper()
	n, err := chromedp.Run(ctx, chromedp.Evaluate[int](js))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
