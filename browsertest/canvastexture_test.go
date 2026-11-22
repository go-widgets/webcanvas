// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// Does the WebGPU canvas texture keep its contents from one frame to the next?
// The spec says no: getCurrentTexture() after the texture expired (at each
// presentation) "Replace[s] the drawing buffer" with a transparent black image
// (WebGPU, section 21.2 GPUCanvasContext, [[currentTexture]] and the "Replace
// the drawing buffer" algorithm). This measures Chrome: frame one clears the
// canvas to opaque red; frame two draws nothing with loadOp "load" and reads the
// canvas back in the same task. The presenter therefore keeps its frame in the
// renderer's own persistent target and copies all of it every frame; a scissor
// on the canvas texture itself would leave the rest transparent.
func TestTheWebGPUCanvasTextureDoesNotKeepItsContents(t *testing.T) {
	url := servePageHTML(t, "./gpupage", `<!doctype html><meta charset="utf-8"><canvas id="screen" width="4" height="4"></canvas>`)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	got, err := chromedp.Run(ctx, chromedp.Evaluate[[]int](`(async () => {
		if (!navigator.gpu) return [-1];
		const adapter = await navigator.gpu.requestAdapter();
		if (!adapter) return [-1];
		const device = await adapter.requestDevice();
		const canvas = document.getElementById("screen");
		const gctx = canvas.getContext("webgpu");
		gctx.configure({ device, format: navigator.gpu.getPreferredCanvasFormat(), alphaMode: "premultiplied" });
		const pass = (loadOp) => {
			const enc = device.createCommandEncoder();
			const p = enc.beginRenderPass({ colorAttachments: [{
				view: gctx.getCurrentTexture().createView(), loadOp, storeOp: "store", clearValue: { r: 1, g: 0, b: 0, a: 1 } }] });
			p.end();
			device.queue.submit([enc.finish()]);
		};
		const frame = () => new Promise((r) => requestAnimationFrame(r));
		await frame();
		const snap = document.createElement("canvas").getContext("2d");
		snap.canvas.width = snap.canvas.height = 4;
		const read = () => { snap.clearRect(0, 0, 4, 4); snap.drawImage(canvas, 0, 0); return Array.from(snap.getImageData(1, 1, 1, 1).data); };
		pass("clear");
		const control = read(); // the readback works: frame one is red
		await frame();
		await frame();
		pass("load");
		return control.concat(read());
	})()`, chromedp.EvalAwaitPromise))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 1 {
		t.Skip("no WebGPU in this browser")
	}
	if got[0] != 255 || got[3] != 255 {
		// The control failed, so the measurement cannot be made here (a software
		// adapter, as on the CI runner, does not read the canvas back): that is
		// no verdict on the texture, so skip rather than fail.
		t.Skipf("control: frame one reads %v, want opaque red: no readback on this adapter", got[:4])
	}
	if got[7] != 0 {
		t.Fatalf("frame two reads %v: the canvas texture kept frame one's red", got[4:])
	}
}
