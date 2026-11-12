// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// enterTouched presses Enter in the middle of a document of the given number
// of lines and returns how many distinct line elements of the layer the press
// created, removed, restyled or rewrote (a MutationObserver on the layer), and
// how many mutation records that made.
func enterTouched(t *testing.T, lines int) (touched, records float64) {
	t.Helper()
	ctx := textPage(t, 1)
	var sb strings.Builder // distinct lines: identical ones would hide a rewrite by index
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "    result := compute(%04d) // line\n", i)
	}
	setText(t, ctx, sb.String()+"last")
	ev[any](t, ctx, `setFocus(true)`)
	time.Sleep(300 * time.Millisecond)
	at := 4 + 35*(lines/4) // column 4 of line lines/4: three quarters of the document follow
	ev[any](t, ctx, fmt.Sprintf(`setSelection(%d, %d)`, at, at))
	time.Sleep(100 * time.Millisecond)
	ev[any](t, ctx, `(() => {
  const el = document.querySelector('[data-webcanvas-layer]');
  const T = window.__touch = {set: new Set(), recs: 0};
  const line = x => { while (x && x.parentNode !== el) x = x.parentNode; return x; };
  window.__mo = new MutationObserver(rs => { for (const r of rs) {
    T.recs++;
    if (r.target === el) { for (const x of [...r.addedNodes, ...r.removedNodes]) T.set.add(x); }
    else { const l = line(r.target); if (l) T.set.add(l); }
  }});
  window.__mo.observe(el, {subtree: true, childList: true, characterData: true, attributes: true});
})()`)
	for _, typ := range []input.DispatchKeyEventType{input.DispatchKeyEventTypeRawKeyDown, input.DispatchKeyEventTypeKeyUp} {
		p := input.DispatchKeyEventParams{Type: typ, Key: "Enter", Code: "Enter", WindowsVirtualKeyCode: 13}
		if _, err := chromedp.Call(ctx, input.DispatchKeyEvent, p); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	got := ev[[]float64](t, ctx, `(() => { window.__mo.disconnect(); return [window.__touch.set.size, window.__touch.recs]; })()`)
	if m := state(t, ctx); strings.Count(m.Text, "\n") != lines+1 {
		t.Fatalf("Enter did not reach the model: %d newlines, want %d", strings.Count(m.Text, "\n"), lines+1)
	}
	if dom := ev[string](t, ctx, layerText); dom != state(t, ctx).Text {
		t.Fatal("DOM and model differ after Enter")
	}
	return got[0], got[1]
}

// The DOM work of one Enter does not grow with the document: on 2000 lines it
// touches the line elements it touches on 200.
func TestTextLayerEnterTouchesBoundedLines(t *testing.T) {
	small, smallRecs := enterTouched(t, 200)
	big, bigRecs := enterTouched(t, 2000)
	t.Logf("line elements touched by one Enter: %g on 200 lines (%g records), %g on 2000 (%g records)", small, smallRecs, big, bigRecs)
	if big > small+2 || big > 12 {
		t.Fatalf("one Enter touched %g line elements on 2000 lines and %g on 200: the work grows with the document", big, small)
	}
}
