// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// Two surfaces among prose. The page patches add/removeEventListener first, so
// the test can read the net number of listeners per event type: what Close
// must bring back to zero.
const surfacesIndex = `<!doctype html><meta charset="utf-8">
<style>body{margin:0} canvas{display:block;width:200px;height:100px}</style>
<script>
window.__net = {};
const add = EventTarget.prototype.addEventListener, rem = EventTarget.prototype.removeEventListener;
EventTarget.prototype.addEventListener = function (t, f, o) { __net[t] = (__net[t] || 0) + 1; return add.call(this, t, f, o); };
EventTarget.prototype.removeEventListener = function (t, f, o) { __net[t] = (__net[t] || 0) - 1; return rem.call(this, t, f, o); };
</script>
<canvas id="a"></canvas>
<canvas id="b"></canvas>
<div style="height:4000px">prose</div>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

func surfacesPage(t *testing.T) context.Context {
	t.Helper()
	url := servePageHTML(t, "./surfacespage", surfacesIndex)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.surfacesMounted === true`)); err == nil && ok {
			return ctx
		}
		if time.Now().After(deadline) {
			t.Fatal("the surfaces never mounted")
		}
	}
}

// logOf is what the named scene heard.
func logOf(t *testing.T, ctx context.Context, name string) []string {
	t.Helper()
	l, err := chromedp.Run(ctx, chromedp.Evaluate[[]string](`((window.__log || {})["`+name+`"] || []).filter(e => !e.startsWith("wheel"))`))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func wheels(t *testing.T, ctx context.Context, name string) int {
	t.Helper()
	n, err := chromedp.Run(ctx, chromedp.Evaluate[int](`((window.__log || {})["`+name+`"] || []).filter(e => e.startsWith("wheel")).length`))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func scrollY(t *testing.T, ctx context.Context) float64 {
	t.Helper()
	y, err := chromedp.Run(ctx, chromedp.Evaluate[float64](`window.scrollY`))
	if err != nil {
		t.Fatal(err)
	}
	return y
}

// wheel turns the real wheel over the page at (x, y), as a mouse would.
func wheel(t *testing.T, ctx context.Context, x, y float64) {
	t.Helper()
	p := input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseWheel, X: x, Y: y, DeltaY: 300,
		// DeltaX is omitted from the JSON when zero, and the protocol wants both.
		DeltaX: 0.01}
	if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, p); err != nil {
		t.Fatal(err)
	}
}

// click presses the left button in the middle of the named canvas (a: y 0-100,
// b: y 100-200, both 200 px wide), which focuses it natively.
func click(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	y := 50.0
	if name == "b" {
		y = 150
	}
	if err := chromedp.Do(ctx, chromedp.MouseClickXY(100, y)); err != nil {
		t.Fatal(err)
	}
}

func resetScroll(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](`window.scrollTo(0, 0)`)); err != nil {
		t.Fatal(err)
	}
}

// A key reaches the surface that has focus and no other.
func TestKeysReachOnlyTheFocusedSurface(t *testing.T) {
	ctx := surfacesPage(t)
	// Nothing has focus yet: a key goes nowhere.
	if err := chromedp.Do(ctx, chromedp.KeyEvent("n")); err != nil {
		t.Fatal(err)
	}
	click(t, ctx, "a")
	if err := chromedp.Do(ctx, chromedp.KeyEvent("x")); err != nil {
		t.Fatal(err)
	}
	if a, b := logOf(t, ctx, "a"), logOf(t, ctx, "b"); !slices.Equal(a, []string{"char x"}) || len(b) != 0 {
		t.Fatalf("after typing x with a focused: a=%q b=%q", a, b)
	}
	click(t, ctx, "b")
	if err := chromedp.Do(ctx, chromedp.KeyEvent("y")); err != nil {
		t.Fatal(err)
	}
	if a, b := logOf(t, ctx, "a"), logOf(t, ctx, "b"); !slices.Equal(a, []string{"char x"}) || !slices.Equal(b, []string{"char y"}) {
		t.Fatalf("after typing y with b focused: a=%q b=%q", a, b)
	}
}

// A wheel over a surface whose scene does not consume it scrolls the page; over
// one that consumes it, the page stays.
func TestWheelScrollsThePageOnlyWhereTheSceneDoesNotConsume(t *testing.T) {
	ctx := surfacesPage(t)

	wheel(t, ctx, 100, 150) // over b: does not consume
	waitScroll(t, ctx, true)
	if wheels(t, ctx, "b") == 0 {
		t.Fatal("b's scene never saw the wheel: the listener is missing, so the page scrolled for the wrong reason")
	}

	resetScroll(t, ctx)
	wheel(t, ctx, 100, 50) // over a: consumes
	time.Sleep(700 * time.Millisecond)
	if y := scrollY(t, ctx); y != 0 {
		t.Fatalf("the page scrolled to %v under a surface that consumed the wheel", y)
	}
	if wheels(t, ctx, "a") == 0 {
		t.Fatal("a's scene never saw the wheel")
	}
}

// waitScroll polls until the page has (or, with false, has not) scrolled.
func waitScroll(t *testing.T, ctx context.Context, want bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if (scrollY(t, ctx) > 0) == want {
			return
		}
	}
	t.Fatalf("scrollY is %v, scrolled=%v wanted", scrollY(t, ctx), want)
}

// Close removes every listener the surface installed, and the others carry on.
func TestCloseRemovesEveryListener(t *testing.T) {
	ctx := surfacesPage(t)
	net := func() map[string]int {
		m, err := chromedp.Run(ctx, chromedp.Evaluate[map[string]int](`window.__net`))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	closeSurface := func(name string) {
		if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](`closeSurface("`+name+`")`)); err != nil {
			t.Fatal(err)
		}
	}
	before := net()
	for _, typ := range []string{"keydown", "mousedown", "wheel"} {
		if before[typ] != 2 {
			t.Fatalf("with two surfaces mounted, %q has %d listeners, want 2 (%v)", typ, before[typ], before)
		}
	}

	closeSurface("a")
	for typ, n := range net() {
		if want := before[typ] / 2; n != want {
			t.Errorf("after closing a, %q has %d listeners, want %d", typ, n, want)
		}
	}
	// a no longer hears a key or a wheel; the page scrolls under it; b still works.
	click(t, ctx, "a")
	if err := chromedp.Do(ctx, chromedp.KeyEvent("z")); err != nil {
		t.Fatal(err)
	}
	wheel(t, ctx, 100, 50)
	waitScroll(t, ctx, true)
	if a := logOf(t, ctx, "a"); len(a) != 0 || wheels(t, ctx, "a") != 0 {
		t.Fatalf("a closed surface heard %q", a)
	}
	resetScroll(t, ctx) // the wheel above moved the page; b is at y 100-200 again
	click(t, ctx, "b")
	if err := chromedp.Do(ctx, chromedp.KeyEvent("y")); err != nil {
		t.Fatal(err)
	}
	if b := logOf(t, ctx, "b"); !slices.Equal(b, []string{"char y"}) {
		t.Fatalf("b, still open, heard %q", b)
	}

	closeSurface("b")
	closeSurface("b") // idempotent
	for typ, n := range net() {
		if n != 0 {
			t.Errorf("after closing both, %q leaks %d listener(s)", typ, n)
		}
	}
}
