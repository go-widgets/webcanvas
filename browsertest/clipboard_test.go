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

// pasteKey presses and releases Cmd+V on the focused element (CDP modifier
// bit 4 is Meta/Command).
func pasteKey(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, typ := range []input.DispatchKeyEventType{input.DispatchKeyEventTypeRawKeyDown, input.DispatchKeyEventTypeKeyUp} {
		p := input.DispatchKeyEventParams{Type: typ, Modifiers: 4, Key: "v", Code: "KeyV", WindowsVirtualKeyCode: 86}
		if _, err := chromedp.Call(ctx, input.DispatchKeyEvent, p); err != nil {
			t.Fatal(err)
		}
	}
}

// waitLog waits until the named scene heard as many events as want, then
// checks it heard exactly want and nothing more.
func waitLog(t *testing.T, ctx context.Context, name string, want []string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if len(logOf(t, ctx, name)) >= len(want) {
			break
		}
	}
	time.Sleep(200 * time.Millisecond) // a second delivery would land by now
	if got := logOf(t, ctx, name); !slices.Equal(got, want) {
		t.Fatalf("%s heard %q, want %q", name, got, want)
	}
}

// Cmd+V on a canvas waits for the paste event: the scene gets the key once,
// after the bridge holds the text the event carried. The page's own listener
// fires the paste event the way a browser's default action does (on the
// focused element, in the keydown's task, before any timer): headless Chrome
// offers no way to choose what its clipboard holds.
func TestPasteShortcutWaitsForThePasteEvent(t *testing.T) {
	ctx := surfacesPage(t)
	click(t, ctx, "b")
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](`(() => {
		document.getElementById("b").addEventListener("keydown", (e) => {
			if (e.key !== "v" || !e.metaKey || e.defaultPrevented) return;
			const dt = new DataTransfer();
			dt.setData("text/plain", "from the OS");
			e.target.dispatchEvent(new ClipboardEvent("paste", {clipboardData: dt, bubbles: true, cancelable: true}));
		});
	})()`)); err != nil {
		t.Fatal(err)
	}
	pasteKey(t, ctx)
	waitLog(t, ctx, "b", []string{"down v clip=from the OS"})
}

// With no paste event the held key still arrives, once, with the clipboard as
// it was: the behaviour before the bridge.
func TestPasteShortcutWithoutPasteEventIsDeliveredOnce(t *testing.T) {
	ctx := surfacesPage(t)
	click(t, ctx, "a")
	pasteKey(t, ctx)
	waitLog(t, ctx, "a", []string{"down v clip="})
}

// Copy: SetClipboardText hands the text to navigator.clipboard.writeText.
func TestCopyReachesTheBrowsersClipboard(t *testing.T) {
	ctx := surfacesPage(t)
	got, err := chromedp.Run(ctx, chromedp.Evaluate[string](`(() => {
		const seen = [];
		navigator.clipboard.writeText = (s) => { seen.push(s); return Promise.resolve(); };
		setClip("copied in the page");
		return seen.join("|");
	})()`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "copied in the page" {
		t.Fatalf("writeText saw %q", got)
	}
}
