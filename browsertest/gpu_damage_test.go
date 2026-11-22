// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// counted wraps GPUQueue.submit before the wasm starts, so the page can be
// asked how many command buffers a frame sent to the GPU.
const countedGPU = `<!doctype html><meta charset="utf-8">
<canvas id="screen"></canvas>
<script>
window.__submits = 0;
// A WebGPU canvas can be read back only in the task that drew it, so every
// submit is followed, in a microtask, by a snapshot of the canvas.
window.__snap = new OffscreenCanvas(1, 1);
if (window.GPUQueue) {
  const submit = GPUQueue.prototype.submit;
  GPUQueue.prototype.submit = function () {
    window.__submits++;
    const r = submit.apply(this, arguments);
    queueMicrotask(() => {
      const c = document.getElementById("screen");
      if (c.width > 1) { __snap.width = c.width; __snap.height = c.height; __snap.getContext("2d").drawImage(c, 0, 0); }
    });
    return r;
  };
}
</script>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// A DamagePainted scene on the GPU presenter: the first frame and every frame
// with damage reach the GPU, a frame with no damage sends no command buffer and
// presents nothing, and the canvas is what a full repaint shows.
func TestGPUPresenterSkipsFramesWithoutDamage(t *testing.T) {
	url := servePageHTML(t, "./gpupage", countedGPU)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	ready(t, ctx)
	if msg, err := chromedp.Run(ctx, chromedp.Evaluate[string](`window.__err()`)); err != nil || msg != "" {
		t.Skipf("no WebGPU in this browser: %q %v", msg, err)
	}
	stats := func() (presented, skipped, paints, probes, submits int) {
		t.Helper()
		n, err := chromedp.Run(ctx, chromedp.Evaluate[[]int](`[...window.__stats(), window.__submits]`))
		if err != nil {
			t.Fatal(err)
		}
		return n[0], n[1], n[2], n[3], n[4]
	}
	frame := func(js string) {
		t.Helper()
		p0, s0, _, _, _ := stats()
		if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](js)); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			p, s, _, _, _ := stats()
			if p+s > p0+s0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the frame never ran")
			}
		}
	}

	p, s, paints, probes, firstSubmits := stats()
	if p != 1 || s != 0 || paints != 1 || probes != 1 || firstSubmits < 2 {
		t.Fatalf("first frame: presented %d skipped %d, Paint %d, PaintDamaged %d, submits %d; want one whole frame", p, s, paints, probes, firstSubmits)
	}
	frame(`window.__recolor(0, 1)`)
	p, s, paints, probes, submits := stats()
	if p != 2 || s != 0 || paints != 2 || probes != 2 || submits <= firstSubmits {
		t.Fatalf("a recoloured box: presented %d skipped %d, Paint %d, PaintDamaged %d, submits %d", p, s, paints, probes, submits)
	}
	before := submits
	for range 3 {
		frame(`window.__idle()`)
	}
	p, s, paints, probes, submits = stats()
	if p != 2 || s != 3 || paints != 2 || probes != 5 || submits != before {
		t.Fatalf("three idle frames: presented %d skipped %d, Paint %d, PaintDamaged %d, submits %d (was %d); want none sent", p, s, paints, probes, submits, before)
	}

	// The canvas equals a full repaint: box 0 green, box 1 yellow, the rest blue.
	frame(`window.__recolor(1, 2)`)
	got, err := chromedp.Run(ctx, chromedp.Evaluate[[][]int](`(() => {
		const c = __snap.getContext("2d");
		return [[5,5],[50,35],[0,0],[63,47],[20,20]].map(([x,y]) => Array.from(c.getImageData(x,y,1,1).data));
	})()`))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{0, 255, 0, 255}, {255, 255, 0, 255}, {0, 0, 255, 255}, {0, 0, 255, 255}, {0, 0, 255, 255}}
	for i := range want {
		for k := range want[i] {
			if got[i][k] != want[i][k] {
				t.Fatalf("canvas pixels %v, want %v", got, want)
			}
		}
	}
}
