// SPDX-License-Identifier: BSD-3-Clause

// Package browsertest runs webcanvas in a real browser -- headless Chrome,
// through chromedp -- for what a native test cannot reach: the wasm host.
// It is a module of its own so that webcanvas itself depends on nothing.
//
//	cd browsertest && go test ./...
//
// Skipped when no Chrome is found.
package browsertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const index = `<!doctype html><meta charset="utf-8">
<canvas id="screen"></canvas>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// serve builds ./page for js/wasm and serves it with Go's own wasm_exec.js.
func serve(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "page.wasm"), "./page")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the page: %v\n%s", err, out)
	}
	execJS, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "wasm_exec.js"), execJS, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644)
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv.URL
}

func browser(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", true))
	if p := os.Getenv("CHROME"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancel)
	ctx, cancel2 := chromedp.NewContext(actx)
	t.Cleanup(cancel2)
	ctx, cancel3 := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel3)
	if _, err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Skipf("no Chrome to run the page in: %v", err)
	}
	return ctx
}

// pixel is the red and blue of the canvas' first pixel.
const pixel = `(() => { const d = document.getElementById("screen").getContext("2d").getImageData(0,0,1,1).data; return [d[0], d[2]]; })()`

// A scene that changes on its own goroutine, with no input event, is drawn
// again once it asks through RepaintWith.
func TestRepaintWithFromAGoroutine(t *testing.T) {
	url := serve(t)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	// Polled by hand: right after a navigation the page's execution context
	// can still be the old one, which a single evaluation reports as an error.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.webcanvasReady === true`)); err == nil && ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the page never drew its first frame")
		}
	}
	first, err := chromedp.Run(ctx, chromedp.Evaluate[[]int](pixel))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	later, err := chromedp.Run(ctx, chromedp.Evaluate[[]int](pixel))
	if err != nil {
		t.Fatal(err)
	}
	if first[0] != 0 || first[1] != 255 {
		t.Fatalf("the first frame is %v, not blue", first)
	}
	if later[0] != 255 || later[1] != 0 {
		t.Fatalf("600 ms later the canvas is %v: the frame the scene asked for was never drawn", later)
	}
}
