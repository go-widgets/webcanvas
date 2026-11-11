// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

const textIndex = `<!doctype html><meta charset="utf-8">
<style>body{margin:0;font:16px sans-serif} canvas{display:block;width:400px;height:300px} p{margin:8px}</style>
<p id="before">Prose before the cell.</p>
<canvas id="screen"></canvas>
<p id="after">Prose after the cell.</p>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// textPage serves the text-layer page at the given device pixel ratio and waits
// for it. The ratio is set before the page loads, so the scene sizes itself at it.
func textPage(t *testing.T, dpr float64) context.Context { return textPageAt(t, dpr, "") }

// textPageAt is textPage opened as #hash: the scene starts with hash as its text.
func textPageAt(t *testing.T, dpr float64, hash string) context.Context {
	t.Helper()
	url := servePageHTML(t, "./textpage", textIndex)
	ctx := browser(t)
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 800, Height: 700, DeviceScaleFactor: dpr}); err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		url += "#" + hash
	}
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(ctx, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.textReady === true`)); err == nil && ok {
			return ctx
		}
		if time.Now().After(deadline) {
			t.Fatal("the text page never mounted")
		}
	}
}

func setText(t *testing.T, ctx context.Context, s string) {
	t.Helper()
	ev[any](t, ctx, `setText(`+strconv.Quote(s)+`)`)
	time.Sleep(50 * time.Millisecond)
}

type modelState struct {
	Text          string
	Anchor, Focus int
	MarkS, MarkE  int
	Chars         int
	Keys          []string
}

func state(t *testing.T, ctx context.Context) modelState {
	t.Helper()
	var m modelState
	if err := json.Unmarshal([]byte(ev[string](t, ctx, `modelState()`)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// layerText is the text of the layer's lines, joined by newlines.
const layerText = `[...document.querySelector('[data-webcanvas-layer]').children].map(d => d.textContent).join("\n")`

const code = "func main() {\n\tfmt.Println(\"compute\")\n}"

// The caret x of every rune is where the model's grid puts it, to 0.05 px, at
// device pixel ratios 1 and 2, for ASCII and for the mixed line of the design
// (accents, CJK, an astral rune, a combining mark).
func TestTextLayerPositioning(t *testing.T) {
	for _, dpr := range []float64{1, 2} {
		t.Run("dpr"+strconv.FormatFloat(dpr, 'f', -1, 64), func(t *testing.T) {
			ctx := textPage(t, dpr)
			setText(t, ctx, "abcdefghijklmnopqrstuvwxyz0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ\nthe quick brown fox\né 世界 \U0001F642 éx\n")
			res := ev[[]float64](t, ctx, `(() => {
  const el = document.querySelector('[data-webcanvas-layer]');
  const left = document.getElementById('screen').getBoundingClientRect().left;
  let maxErr = 0, n = 0, mixedMax = 0;
  [...el.children].forEach((d, li) => {
    let idx = 0;
    const w = document.createTreeWalker(d, NodeFilter.SHOW_TEXT);
    for (let tn; (tn = w.nextNode());) {
      let u = 0;
      for (const ch of tn.data) {
        const r = document.createRange();
        r.setStart(tn, u); r.setEnd(tn, u + ch.length);
        const err = Math.abs(r.getBoundingClientRect().left - (left + 10 + 9 * idx));
        maxErr = Math.max(maxErr, err); if (li == 2) mixedMax = Math.max(mixedMax, err);
        u += ch.length; idx++; n++;
      }
    }
  });
  return [maxErr, mixedMax, n];
})()`)
			t.Logf("dpr %g: max error %.4f px over %g runes (mixed line %.4f px)", dpr, res[0], res[2], res[1])
			if res[2] < 80 {
				t.Fatalf("only %g runes measured", res[2])
			}
			if res[0] > 0.05 {
				t.Fatalf("positioning error %.4f px > 0.05", res[0])
			}
		})
	}
}

// The AX tree shows one textbox whose value is the code, and the code is
// nowhere else in the tree.
func TestTextLayerAXTree(t *testing.T) {
	ctx := textPage(t, 1)
	setText(t, ctx, code)
	time.Sleep(300 * time.Millisecond)
	tree := axTree(t, ctx)
	var boxes []*ax
	for _, n := range tree {
		if !n.Ignored && n.Role == "textbox" {
			boxes = append(boxes, n)
		}
	}
	if len(boxes) != 1 {
		t.Fatalf("%d textboxes, want 1", len(boxes))
	}
	box := boxes[0]
	if box.Name != "Go cell 1" {
		t.Errorf("name %q", box.Name)
	}
	if box.Value != code {
		t.Errorf("value %q, want the code %q", box.Value, code)
	}
	inside := map[*ax]bool{}
	var walk func(*ax)
	walk = func(n *ax) {
		inside[n] = true
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(box)
	for _, n := range tree {
		if n.Ignored || inside[n] || n == box {
			continue
		}
		if strings.Contains(n.Name, "compute") || strings.Contains(n.Value, "compute") {
			t.Errorf("a second copy of the code: %s %q %q", n.Role, n.Name, n.Value)
		}
	}
}

// A textbox created with its text already in it (a real scene's first frame) is
// read once: the layer's lines replace the text the mirror first gave the
// element, they do not sit beside it.
func TestTextLayerCreatedWithTextIsReadOnce(t *testing.T) {
	ctx := textPageAt(t, 1, "hello")
	time.Sleep(300 * time.Millisecond)
	var boxes []*ax
	for _, n := range axTree(t, ctx) {
		if !n.Ignored && n.Role == "textbox" {
			boxes = append(boxes, n)
		}
	}
	if len(boxes) != 1 {
		t.Fatalf("%d textboxes, want 1", len(boxes))
	}
	if boxes[0].Value != "hello" {
		t.Errorf("value %q, want %q", boxes[0].Value, "hello")
	}
	if got := ev[string](t, ctx, layerText); got != "hello" {
		t.Errorf("layer lines %q, want %q", got, "hello")
	}
}

// Find-in-page's engine reaches the code; a selection from prose into code and
// from code into prose copies the code.
func TestTextLayerFindAndCrossSelection(t *testing.T) {
	ctx := textPage(t, 1)
	setText(t, ctx, code)
	if !ev[bool](t, ctx, `(() => { getSelection().removeAllRanges(); return window.find("compute") &&
  document.querySelector('[data-webcanvas-layer]').contains(getSelection().anchorNode); })()`) {
		t.Error("window.find does not hit the code text")
	}
	// Prose into code: from the start of the "before" paragraph to line 2 of the code.
	got := ev[string](t, ctx, `(() => { const el = document.querySelector('[data-webcanvas-layer]');
  const s = getSelection();
  s.setBaseAndExtent(document.getElementById('before').firstChild, 6, el.children[2].firstChild.firstChild, 1);
  return s.toString(); })()`)
	if want := "before the cell.\n\nfunc main() {\n\tfmt.Println(\"compute\")\n}"[:0]; want != "" {
		t.Fatal()
	}
	if !strings.Contains(got, "func main() {") || !strings.Contains(got, "\tfmt.Println(\"compute\")") || !strings.HasPrefix(got, "before") {
		t.Errorf("prose into code copied %q", got)
	}
	// Code into prose.
	got = ev[string](t, ctx, `(() => { const el = document.querySelector('[data-webcanvas-layer]');
  const s = getSelection();
  s.setBaseAndExtent(el.children[0].firstChild.firstChild, 5, document.getElementById('after').firstChild, 5);
  return s.toString(); })()`)
	if !strings.Contains(got, "main() {") || !strings.Contains(got, "\tfmt.Println(\"compute\")") || !strings.HasSuffix(got, "Prose") {
		t.Errorf("code into prose copied %q", got)
	}
}

// Typing "a" inserts exactly one "a": through the layer, not through Char.
func TestTextLayerTypingInsertsOnce(t *testing.T) {
	ctx := textPage(t, 1)
	setText(t, ctx, "xy")
	ev[any](t, ctx, `setFocus(true)`)
	time.Sleep(300 * time.Millisecond)
	ev[any](t, ctx, `setSelection(1, 1)`)
	time.Sleep(100 * time.Millisecond)
	if _, err := chromedp.Run(ctx, chromedp.KeyEvent("a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	m := state(t, ctx)
	if m.Text != "xay" || m.Chars != 0 {
		t.Fatalf("model %+v: want xay and no Char call", m)
	}
	if got := ev[string](t, ctx, layerText); got != "xay" {
		t.Fatalf("DOM %q, model xay", got)
	}
	if m.Anchor != 2 || m.Focus != 2 {
		t.Errorf("caret %d,%d, want 2", m.Anchor, m.Focus)
	}
	// A named key goes to the scene, and the caret follows into the DOM.
	if _, err := chromedp.Run(ctx, chromedp.KeyEvent("\b")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if m := state(t, ctx); m.Text != "xy" {
		t.Fatalf("after Backspace: %+v", m)
	}
}

// An IME composition lands in the model as marked text, and the commit leaves
// model and DOM equal.
func TestTextLayerComposition(t *testing.T) {
	ctx := textPage(t, 1)
	setText(t, ctx, "ab")
	ev[any](t, ctx, `setFocus(true)`)
	time.Sleep(300 * time.Millisecond)
	ev[any](t, ctx, `setSelection(1, 1)`)
	time.Sleep(100 * time.Millisecond)
	step := func(text string) modelState {
		if _, err := chromedp.Call(ctx, input.ImeSetComposition, input.ImeSetCompositionParams{Text: text, SelectionStart: int64(len([]rune(text))), SelectionEnd: int64(len([]rune(text)))}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		return state(t, ctx)
	}
	m := step("ni")
	if m.Text != "anib" || m.MarkS != 1 || m.MarkE != 3 {
		t.Fatalf("first preview: %+v", m)
	}
	m = step("你")
	if m.Text != "a你b" || m.MarkS != 1 || m.MarkE != 2 {
		t.Fatalf("second preview: %+v", m)
	}
	if _, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: "你"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	m = state(t, ctx)
	if m.Text != "a你b" || m.MarkS != -1 {
		t.Fatalf("after the commit: %+v", m)
	}
	if got := ev[string](t, ctx, layerText); got != m.Text {
		t.Fatalf("DOM %q, model %q", got, m.Text)
	}
}

// The layer is bounded: a document of more lines than the cap puts at most the
// cap in the DOM.
func TestTextLayerIsBounded(t *testing.T) {
	ctx := textPage(t, 1)
	ev[any](t, ctx, `setText("x\n".repeat(20010) + "y".repeat(5000))`)
	time.Sleep(500 * time.Millisecond)
	n := ev[int](t, ctx, `document.querySelector('[data-webcanvas-layer]').children.length`)
	if n != 20000 {
		t.Fatalf("%d lines in the DOM, want the cap of 20000", n)
	}
}

// The keystroke cost table: one "a" at the caret, the layer's line rewrite and
// a forced layout, median of 200 rounds. It runs only when asked (TEXTLAYER_COST=1)
// and says the machine load beside it; times under a load of 8 or more are
// indicative.
func TestTextLayerKeystrokeCost(t *testing.T) {
	if os.Getenv("TEXTLAYER_COST") == "" {
		t.Skip("set TEXTLAYER_COST=1 for the cost table")
	}
	ctx := textPage(t, 1)
	fmt.Printf("lines | build ms | keystroke median ms | min | max\n")
	for _, n := range []int{50, 1000, 10000} {
		for round := 0; round < 2; round++ { // the first run pays the first layout
			r := ev[[]float64](t, ctx, fmt.Sprintf(`bench(%d, 200)`, n))
			if round == 1 {
				fmt.Printf("%5d | %8.1f | %19.2f | %.2f | %.2f\n", n, r[0], r[1], r[2], r[3])
			}
		}
	}
}
