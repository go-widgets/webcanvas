// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// The keystroke cost of the text layer: the time from the browser's first event
// of a key press (keydown, beforeinput, compositionupdate or paste, in the
// capture phase on window) to the last of three updates, all taken in the page
// with performance.now():
//
//   - the model: the last ReplaceInput / SetMarkedInput / UnmarkInput / KeyDown
//     the scene heard;
//   - the DOM layer: the last mutation inside [data-webcanvas-layer];
//   - the canvas: the first Draw after the model changed.
//
// A frame's presentation to the screen is not included, and neither is the
// CDP round trip. The result is the median of b.N presses, reported as ms/op
// (the default ns/op of a benchmark includes the setup and means nothing here).
//
//	cd browsertest && go test -c -o kc.test . && ./kc.test -test.run '^$' \
//	    -test.bench BenchmarkKeystroke -test.benchtime 30x
//
// Under abbench, pass the same binary as --base and --head to get absolute
// numbers at a bounded load.

const keystrokeJS = `(() => {
  const T = window.__t = {start: [], model: [], mut: [], draw: [], last: 0};
  window.__stamp = k => { const n = performance.now(); T[k].push(n); T.last = n; };
  window.__reset = () => { for (const k of ["start","model","mut","draw"]) T[k].length = 0; T.last = performance.now(); };
  for (const ev of ["keydown","beforeinput","compositionstart","compositionupdate","paste"]) {
    window.addEventListener(ev, () => { if (!T.start.length) window.__stamp("start"); }, true);
  }
  new MutationObserver(recs => {
    if (recs.some(r => (r.target.nodeType === 1 ? r.target : r.target.parentElement)?.closest("[data-webcanvas-layer]"))) window.__stamp("mut");
  }).observe(document.body, {subtree: true, childList: true, characterData: true});
  // quiet: every update seen, and nothing stamped for 40 ms.
  window.__quiet = () => {
    if (!T.start.length || !T.model.length || !T.mut.length) return false;
    const m = T.model[T.model.length - 1];
    return T.draw.some(d => d >= m) && performance.now() - T.last > 40;
  };
  window.__cost = () => {
    const s = T.start[0], m = T.model[T.model.length - 1];
    const d = T.draw.find(d => d >= m), u = T.mut[T.mut.length - 1];
    return [Math.max(m, d, u) - s, m - s, u - s, d - s];
  };
})()`

type keyOp struct {
	name string
	// prep is untimed work that puts the caret and the text where the press needs them.
	prep func(t testing.TB, ctx pressCtx)
	// press is the measured event; want is the change in the model's rune count.
	press func(t testing.TB, ctx pressCtx)
	want  int
}

type pressCtx = context.Context

const pasteText = "line one of pasted text\nline two of pasted text\nline three of pasted text\nline four of pasted text\nline five"

func BenchmarkKeystroke(b *testing.B) {
	if testing.Short() {
		b.Skip("timing")
	}
	ops := []keyOp{
		{name: "char", press: func(t testing.TB, c pressCtx) { key(t, c, "a") }, want: 1},
		{name: "enter", press: func(t testing.TB, c pressCtx) {
			// rawKeyDown with no text: the scene takes Enter and the keydown is
			// cancelled, as a browser would not then send a char event.
			for _, typ := range []input.DispatchKeyEventType{input.DispatchKeyEventTypeRawKeyDown, input.DispatchKeyEventTypeKeyUp} {
				p := input.DispatchKeyEventParams{Type: typ, Key: "Enter", Code: "Enter", WindowsVirtualKeyCode: 13}
				if _, err := chromedp.Call(c, input.DispatchKeyEvent, p); err != nil {
					t.Fatal(err)
				}
			}
		}, want: 1},
		{name: "backspace",
			prep:  func(t testing.TB, c pressCtx) { key(t, c, "a"); quiet(t, c) },
			press: func(t testing.TB, c pressCtx) { key(t, c, "\b") }, want: -1},
		{name: "ime", press: func(t testing.TB, c pressCtx) {
			if _, err := chromedp.Call(c, input.ImeSetComposition, input.ImeSetCompositionParams{Text: "ni", SelectionStart: 2, SelectionEnd: 2}); err != nil {
				t.Fatal(err)
			}
		}, want: 2},
		{name: "paste", press: func(t testing.TB, c pressCtx) {
			// Headless Chrome cannot choose its clipboard, so the beforeinput a
			// paste would produce is dispatched on the layer: the timing starts
			// there, after the browser's own paste handling.
			ev[any](t, c, `(() => { const dt = new DataTransfer(); dt.setData("text/plain", `+strconv.Quote(pasteText)+`);
				document.activeElement.dispatchEvent(new InputEvent("beforeinput", {inputType: "insertFromPaste", dataTransfer: dt, bubbles: true, cancelable: true})); })()`)
		}, want: utf8.RuneCountInString(pasteText)},
	}
	for _, lines := range []int{20, 2000} {
		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) {
			var sb strings.Builder
			var starts []int // rune offset of column 4 of each line
			off := 0
			for i := 0; i < lines; i++ {
				if i > 0 {
					sb.WriteByte('\n')
					off++
				}
				starts = append(starts, off+4)
				l := "    result := compute(" + strconv.Itoa(i) + ") // line " + strconv.Itoa(i)
				sb.WriteString(l)
				off += len(l)
			}
			for _, op := range ops {
				b.Run(op.name, func(b *testing.B) {
					// A page of its own per row: an IME composition sent to a page
					// that earlier keys went through is not delivered reliably.
					ctx := textPage(b, 1)
					ev[any](b, ctx, keystrokeJS)
					setText(b, ctx, sb.String())
					ev[any](b, ctx, `setFocus(true)`)
					time.Sleep(300 * time.Millisecond)
					var ms []float64
					for i := 0; i < b.N; i++ {
						at := starts[(i*7919)%lines]
						ev[any](b, ctx, fmt.Sprintf(`setSelection(%d, %d)`, at, at))
						time.Sleep(40 * time.Millisecond)
						before := utf8.RuneCountInString(state(b, ctx).Text)
						if op.prep != nil {
							ev[any](b, ctx, `__reset()`)
							op.prep(b, ctx)
							before++ // the prep types one rune
						}
						ev[any](b, ctx, `__reset()`)
						op.press(b, ctx)
						quiet(b, ctx)
						c := ev[[]float64](b, ctx, `__cost()`)
						if i == 0 { // once per run: the press did what the row says
							m := state(b, ctx)
							got := utf8.RuneCountInString(m.Text) - before
							if got != op.want {
								b.Fatalf("%s: the model changed by %d runes, want %d (%+v)", op.name, got, op.want, m.Keys)
							}
							if dom := ev[string](b, ctx, layerText); dom != m.Text {
								b.Fatalf("%s: DOM and model differ after the press", op.name)
							}
						}
						ms = append(ms, c[0])
						if op.name == "ime" { // commit untimed, so the next press starts clean
							if _, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: "你"}); err != nil {
								b.Fatal(err)
							}
							time.Sleep(80 * time.Millisecond)
						}
					}
					slices.Sort(ms)
					b.ReportMetric(ms[len(ms)/2], "ms/op")
					b.ReportMetric(ms[len(ms)-1], "max-ms/op")
				})
			}
		})
	}
}

func key(t testing.TB, ctx pressCtx, k string) {
	t.Helper()
	if _, err := chromedp.Run(ctx, chromedp.KeyEvent(k)); err != nil {
		t.Fatal(err)
	}
}

// quiet waits until the page has seen the model, the layer and the canvas update.
func quiet(t testing.TB, ctx pressCtx) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ev[bool](t, ctx, `__quiet()`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the press never reached the model, the layer and the canvas: %s, keys %v",
				ev[string](t, ctx, `JSON.stringify(__t)`), state(t, ctx).Keys)
		}
	}
}
