// SPDX-License-Identifier: BSD-3-Clause

package browsertest

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

const a11yIndex = `<!doctype html><meta charset="utf-8">
<style>body{margin:0} canvas{display:block}</style>
<canvas id="screen"></canvas>
<script src="wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("page.wasm"), go.importObject).then(r => go.run(r.instance));
</script>`

// a11yPage serves the mirror page and waits until it is mounted.
func a11yPage(t *testing.T) context.Context {
	t.Helper()
	url := servePageHTML(t, "./a11ypage", a11yIndex)
	ctx := browser(t)
	if _, err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		t.Fatal(err)
	}
	// A headless page is not "focused", and then focus() fires no focus event:
	// emulate a focused page, as a user's window is.
	if _, err := chromedp.Call(ctx, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.a11yReady === true`)); err == nil && ok {
			return ctx
		}
		if time.Now().After(deadline) {
			t.Fatal("the a11y page never mounted")
		}
	}
}

type node struct {
	Key, Role, Name, Value string
	X, Y, W, H             int
	Focused, Disabled      bool
	ReadOnly, Multiline    bool
	HasRange               bool
	Min, Max, Now          float64
}

func ev[T any](t *testing.T, ctx context.Context, js string) T {
	t.Helper()
	v, err := chromedp.Run(ctx, chromedp.Evaluate[T](js))
	if err != nil {
		t.Fatalf("%s: %v", js, err)
	}
	return v
}

// setNodes hands the scene a tree and draws a frame. throttle waits out the
// 200 ms publish interval first, so the frame is published at once.
func setNodes(t *testing.T, ctx context.Context, ns []node) {
	t.Helper()
	time.Sleep(260 * time.Millisecond)
	setNodesNow(t, ctx, ns)
}

func setNodesNow(t *testing.T, ctx context.Context, ns []node) {
	t.Helper()
	b, err := json.Marshal(ns)
	if err != nil {
		t.Fatal(err)
	}
	if r := ev[any](t, ctx, `setNodes(`+strconv.Quote(string(b))+`)`); r != nil {
		t.Fatalf("setNodes: %v", r)
	}
}

// ax is the accessibility tree as Chromium computes it.
type ax struct {
	Role, Name, Value string
	ID                cdp.BackendNodeID
	Ignored           bool
	Props             map[string]string
	Children          []*ax
}

func str(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	if s, err := strconv.Unquote(string(v.Value)); err == nil {
		return s
	}
	return string(v.Value)
}

func axTree(t *testing.T, ctx context.Context) []*ax {
	t.Helper()
	res, err := chromedp.Call(ctx, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[accessibility.NodeID]*ax{}
	var all []*ax
	for _, n := range res.Nodes {
		a := &ax{Role: str(n.Role), Name: str(n.Name), Value: str(n.Value), ID: n.BackendDOMNodeID, Ignored: n.Ignored, Props: map[string]string{}}
		for _, p := range n.Properties {
			a.Props[string(p.Name)] = str(p.Value)
		}
		byID[n.NodeID] = a
		all = append(all, a)
	}
	for _, n := range res.Nodes {
		for _, c := range n.ChildIDs {
			if child := byID[c]; child != nil {
				byID[n.NodeID].Children = append(byID[n.NodeID].Children, child)
			}
		}
	}
	return all
}

// find returns the unignored AX node with that role and name.
func find(t *testing.T, tree []*ax, role, name string) *ax {
	t.Helper()
	for _, n := range tree {
		if !n.Ignored && n.Role == role && n.Name == name {
			return n
		}
	}
	var got []string
	for _, n := range tree {
		if !n.Ignored {
			got = append(got, n.Role+":"+n.Name)
		}
	}
	t.Fatalf("no %s %q in the AX tree: %q", role, name, got)
	return nil
}

func text(n *ax) string {
	var sb strings.Builder
	var walk func(*ax)
	walk = func(a *ax) {
		if a.Role == "StaticText" || a.Role == "InlineTextBox" {
			if a.Role == "StaticText" {
				sb.WriteString(a.Name)
			}
			return
		}
		for _, c := range a.Children {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func boxOf(t *testing.T, ctx context.Context, id cdp.BackendNodeID) (x, y, w, h float64) {
	t.Helper()
	m, err := chromedp.Call(ctx, dom.GetBoxModel, dom.GetBoxModelParams{BackendNodeID: id})
	if err != nil {
		t.Fatal(err)
	}
	q := m.Model.Border
	return q[0], q[1], q[2] - q[0], q[5] - q[1]
}

var sample = []node{
	{Key: "Button#0", Role: "button", Name: "Save", X: 10, Y: 20, W: 80, H: 30},
	{Key: "Checkbox#0", Role: "checkbox", Name: "Wrap lines", Value: "checked", X: 10, Y: 60, W: 100, H: 20},
	{Key: "Slider#0", Role: "slider", Name: "Volume", Value: "2.5 of 10", X: 10, Y: 90, W: 120, H: 16, HasRange: true, Max: 10, Now: 2.5},
	{Key: "@cell3", Role: "textbox", Name: "Go cell 3", Value: "x := 1\ny := 2", X: 150, Y: 20, W: 200, H: 80, Multiline: true},
	{Key: "Label#0", Role: "text", Name: "Hello world", X: 150, Y: 120, W: 90, H: 14},
	{Key: "Switch#0", Role: "switch", Name: "Dark", X: 150, Y: 150, W: 40, H: 20, Disabled: true},
}

// Chromium's accessibility tree shows every node with its role, name, value and
// bounds -- and the mirror is not hidden from it.
func TestMirrorTreeHasRoleNameValueAndBounds(t *testing.T) {
	ctx := a11yPage(t)
	setNodes(t, ctx, sample)
	tree := axTree(t, ctx)

	btn := find(t, tree, "button", "Save")
	box := find(t, tree, "checkbox", "Wrap lines")
	sld := find(t, tree, "slider", "Volume")
	txt := find(t, tree, "textbox", "Go cell 3")
	find(t, tree, "StaticText", "Hello world")
	sw := find(t, tree, "switch", "Dark")

	if box.Props["checked"] != "true" {
		t.Errorf("checkbox checked = %q, want true", box.Props["checked"])
	}
	if sld.Value != "2.5" || sld.Props["valuemin"] != "0" || sld.Props["valuemax"] != "10" {
		t.Errorf("slider value %q min %q max %q", sld.Value, sld.Props["valuemin"], sld.Props["valuemax"])
	}
	if vt := ev[string](t, ctx, `document.querySelector("[data-webcanvas-mirror] [role=slider]").getAttribute("aria-valuetext")`); vt != "2.5 of 10" {
		t.Errorf("slider aria-valuetext = %q", vt)
	}
	if got := text(txt); got != "x := 1\ny := 2" {
		t.Errorf("textbox text = %q", got)
	}
	if txt.Props["multiline"] != "true" {
		t.Errorf("textbox multiline = %q", txt.Props["multiline"])
	}
	if sw.Props["disabled"] != "true" {
		t.Errorf("disabled switch: %v", sw.Props)
	}

	// Bounds: the box the browser reports is the node's, in CSS pixels (the
	// canvas is at the page origin, unscaled).
	for _, c := range []struct {
		a    *ax
		n    node
		name string
	}{{btn, sample[0], "button"}, {box, sample[1], "checkbox"}, {sld, sample[2], "slider"}, {txt, sample[3], "textbox"}, {sw, sample[5], "switch"}} {
		x, y, w, h := boxOf(t, ctx, c.a.ID)
		if math.Abs(x-float64(c.n.X)) > 0.5 || math.Abs(y-float64(c.n.Y)) > 0.5 || math.Abs(w-float64(c.n.W)) > 0.5 || math.Abs(h-float64(c.n.H)) > 0.5 {
			t.Errorf("%s bounds = %.1f,%.1f %.1fx%.1f, want %d,%d %dx%d", c.name, x, y, w, h, c.n.X, c.n.Y, c.n.W, c.n.H)
		}
	}
}

// The mirror is hidden by clipping, never by what takes it out of the
// accessibility tree.
func TestMirrorIsClippedNotHidden(t *testing.T) {
	ctx := a11yPage(t)
	setNodes(t, ctx, sample)
	got := ev[map[string]string](t, ctx, `(() => { const r = document.querySelector("[data-webcanvas-mirror]"), c = getComputedStyle(r);
		return {display: c.display, visibility: c.visibility, opacity: c.opacity, clipPath: c.clipPath, ariaHidden: String(r.getAttribute("aria-hidden")),
		        pointer: c.pointerEvents, overflow: c.overflow,
		        hit: String(document.elementFromPoint(20, 30) === document.getElementById("screen"))}; })()`)
	want := map[string]string{"display": "block", "visibility": "visible", "opacity": "1", "clipPath": "inset(50%)", "ariaHidden": "null",
		"pointer": "none", "overflow": "hidden", "hit": "true"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (the mouse must reach the canvas, the mirror must stay exposed)", k, got[k], v)
		}
	}
	for _, n := range axTree(t, ctx) {
		if n.Role == "button" && n.Name == "Save" && n.Ignored {
			t.Error("the mirror's button is ignored by the accessibility tree")
		}
	}
}

// An element whose Key is unchanged keeps the same DOM node -- the same backend
// node id -- whatever else changes around it.
func TestMirrorKeepsBackendNodeIdsAcrossFrames(t *testing.T) {
	ctx := a11yPage(t)
	mk := func(k string, x int) node {
		return node{Key: k, Role: "button", Name: "btn " + k, X: x, Y: 10, W: 40, H: 20}
	}
	setNodes(t, ctx, []node{mk("a", 0), mk("b", 50), mk("c", 100), mk("d", 150)})
	tree := axTree(t, ctx)
	before := map[string]cdp.BackendNodeID{}
	for _, k := range []string{"a", "b", "c", "d"} {
		before[k] = find(t, tree, "button", "btn "+k).ID
	}
	// Frame 2: c moves to the front, b moves and is disabled, d goes, e comes.
	b := mk("b", 60)
	b.Disabled = true
	setNodes(t, ctx, []node{mk("c", 0), mk("a", 10), b, mk("e", 200)})
	tree = axTree(t, ctx)
	for _, k := range []string{"a", "b", "c"} {
		if got := find(t, tree, "button", "btn "+k).ID; got != before[k] {
			t.Errorf("key %q: backend node id %d became %d, the element was recreated", k, before[k], got)
		}
	}
	if e := find(t, tree, "button", "btn e").ID; slices.Contains([]cdp.BackendNodeID{before["a"], before["b"], before["c"], before["d"]}, e) {
		t.Error("the new key reused an old node")
	}
	if bb := find(t, tree, "button", "btn b"); bb.Props["disabled"] != "true" {
		t.Errorf("b was not updated: %v", bb.Props)
	}
	// Order in the DOM is the tree's order.
	order := ev[[]string](t, ctx, `[...document.querySelectorAll("[data-webcanvas-mirror] button")].map(b => b.textContent)`)
	if !slices.Equal(order, []string{"btn c", "btn a", "btn b", "btn e"}) {
		t.Errorf("DOM order %q", order)
	}
	// Frame 3: a change of text on one key keeps its element too.
	c := mk("c", 0)
	c.Name = "renamed"
	setNodes(t, ctx, []node{c, mk("a", 10), b, mk("e", 200)})
	if got := find(t, axTree(t, ctx), "button", "renamed").ID; got != before["c"] {
		t.Errorf("renaming kept id %d, got %d", before["c"], got)
	}
}

// A click event on a mirror element -- what a screen reader's "activate" sends
// -- replays a press and release at the node's centre; a focus from assistive
// technology does too; DOM focus follows toolkit focus; and keys typed with a
// mirror element focused reach the scene.
func TestMirrorActivationFocusAndKeys(t *testing.T) {
	ctx := a11yPage(t)
	setNodes(t, ctx, sample)
	clicks := func() []string { return ev[[]string](t, ctx, `window.__clicks || []`) }

	ev[any](t, ctx, `document.querySelector("[data-webcanvas-mirror] button").click()`)
	if got := clicks(); !slices.Equal(got, []string{"click 50,35", "release 50,35"}) {
		t.Fatalf("a click on the Save button replayed %q", got)
	}

	// A focus the scene did not give: assistive technology moved it, so it is a click.
	ev[any](t, ctx, `window.__clicks = []; document.querySelector("[data-webcanvas-mirror] [role=checkbox]").focus()`)
	if got := clicks(); !slices.Equal(got, []string{"click 60,70", "release 60,70"}) {
		t.Fatalf("a focus on the checkbox replayed %q", got)
	}

	// Toolkit focus on the slider: DOM focus follows (once the surface holds it).
	ev[any](t, ctx, `window.__clicks = []; focusSurface()`)
	s := slices.Clone(sample)
	s[2].Focused = true
	setNodesNow(t, ctx, s) // a focus change is never throttled
	if got := ev[string](t, ctx, `document.activeElement.getAttribute("role") + ":" + document.activeElement.getAttribute("aria-label")`); got != "slider:Volume" {
		t.Fatalf("DOM focus is on %q, want the slider", got)
	}
	if got := clicks(); len(got) != 0 {
		t.Fatalf("the glue's own focus() was taken for assistive technology: %q", got)
	}
	// ...and a key typed there reaches the scene.
	if err := chromedp.Do(ctx, chromedp.KeyEvent(kb.ArrowRight)); err != nil {
		t.Fatal(err)
	}
	if keys := ev[[]string](t, ctx, `window.__keys || []`); !slices.Equal(keys, []string{"down ArrowRight"}) {
		t.Fatalf("keys heard: %q", keys)
	}
	// Focus leaves the widgets: it goes back to the mirror root.
	setNodesNow(t, ctx, sample)
	if got := ev[bool](t, ctx, `document.activeElement === document.querySelector("[data-webcanvas-mirror]")`); !got {
		t.Error("with no focused node, DOM focus should rest on the mirror root")
	}
}

// A status text is announced from the persistent live region, and an alert
// from the assertive one; both regions exist, empty, before any announcement.
func TestMirrorAnnouncesFromPersistentLiveRegions(t *testing.T) {
	ctx := a11yPage(t)
	regions := `(() => { const r = document.querySelector("[data-webcanvas-mirror]");
		return [r.children[0].textContent, r.children[1].textContent, r.children[0].getAttribute("aria-live"), r.children[1].getAttribute("aria-live")]; })()`
	if got := ev[[]string](t, ctx, regions); !slices.Equal(got, []string{"", "", "polite", "assertive"}) {
		t.Fatalf("regions before any announcement: %q (they must exist, empty, first)", got)
	}
	// The scene's first frame was empty, so a status that appears is news.
	setNodes(t, ctx, []node{{Key: "Status#0", Role: "status", Name: "Ready", X: 0, Y: 0, W: 50, H: 10}})
	if got := ev[[]string](t, ctx, regions); got[0] != "Ready" {
		t.Fatalf("a status that appeared: %q", got)
	}
	ev[any](t, ctx, `window.__polite = document.querySelector("[data-webcanvas-mirror]").children[0]`)

	setNodes(t, ctx, []node{{Key: "Status#0", Role: "status", Name: "Saved", X: 0, Y: 0, W: 50, H: 10}})
	if got := ev[[]string](t, ctx, regions); !slices.Equal(got, []string{"Saved", "", "polite", "assertive"}) {
		t.Fatalf("regions after a status change: %q", got)
	}
	setNodes(t, ctx, []node{
		{Key: "Status#0", Role: "status", Name: "Saved", X: 0, Y: 0, W: 50, H: 10},
		{Key: "Alert#0", Role: "alert", Name: "Disk full", X: 0, Y: 20, W: 50, H: 10},
	})
	if got := ev[[]string](t, ctx, regions); !slices.Equal(got, []string{"Saved", "Disk full", "polite", "assertive"}) {
		t.Fatalf("regions after an alert: %q", got)
	}
	if !ev[bool](t, ctx, `window.__polite === document.querySelector("[data-webcanvas-mirror]").children[0]`) {
		t.Error("the polite region was recreated: it is not persistent")
	}
	tree := axTree(t, ctx)
	var live []string
	for _, n := range tree {
		if !n.Ignored && (n.Role == "status" || n.Role == "alert") && n.Props["live"] != "" {
			live = append(live, n.Role+"="+n.Props["live"]+":"+text(n))
		}
	}
	for _, want := range []string{"status=polite:Saved", "alert=assertive:Disk full"} {
		if !slices.Contains(live, want) {
			t.Errorf("AX live regions %q lack %q", live, want)
		}
	}
}

// Names and values come from the app and may be untrusted: markup in them is
// text, never an element, and never script.
func TestMirrorTreatsMarkupAsData(t *testing.T) {
	ctx := a11yPage(t)
	evil := `<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script>"'>`
	setNodes(t, ctx, []node{
		{Key: "b", Role: "button", Name: evil, X: 0, Y: 0, W: 50, H: 20},
		{Key: "t", Role: "textbox", Name: evil, Value: evil, X: 0, Y: 30, W: 50, H: 20},
		{Key: "c", Role: "checkbox", Name: evil, X: 0, Y: 60, W: 50, H: 20},
		{Key: "r", Role: "x\" onclick=\"window.__pwned=3", Name: evil, X: 0, Y: 90, W: 50, H: 20},
		{Key: "s", Role: "status", Name: evil, X: 0, Y: 120, W: 50, H: 20},
	})
	time.Sleep(300 * time.Millisecond) // an error handler, were there one, would have run by now
	setNodes(t, ctx, []node{
		{Key: "b", Role: "button", Name: evil + "2", X: 0, Y: 0, W: 50, H: 20},
		{Key: "s", Role: "status", Name: evil + "3", X: 0, Y: 120, W: 50, H: 20},
	})
	time.Sleep(300 * time.Millisecond)
	got := ev[map[string]any](t, ctx, `(() => { const r = document.querySelector("[data-webcanvas-mirror]");
		return {imgs: r.querySelectorAll("img,script").length, pwned: String(window.__pwned),
		        btn: r.querySelector("button").textContent, polite: r.children[0].textContent,
		        names: [...r.querySelectorAll("*")].flatMap(e => [...e.attributes].map(a => a.name)).filter(n => n.startsWith("on")).length}; })()`)
	if got["imgs"] != 0.0 || got["pwned"] != "undefined" || got["names"] != 0.0 {
		t.Fatalf("markup became active: %v", got)
	}
	if got["btn"] != evil+"2" || got["polite"] != evil+"3" {
		t.Fatalf("the text was altered: %v", got)
	}
	if n := find(t, axTree(t, ctx), "button", evil+"2"); n == nil {
		t.Fatal("unreachable")
	}
}

// Ten frames in 50 ms publish at most twice (now, and once more when the
// interval is up) and the DOM ends on the last tree; an unchanged tree touches
// the DOM not at all.
func TestMirrorThrottle(t *testing.T) {
	ctx := a11yPage(t)
	setNodes(t, ctx, []node{{Key: "a", Role: "button", Name: "n0", X: 0, Y: 0, W: 20, H: 20}})
	ev[any](t, ctx, `window.__seen = []; new MutationObserver(() => { const b = document.querySelector("[data-webcanvas-mirror] button"); if (b) window.__seen.push(b.textContent); })
		.observe(document.querySelector("[data-webcanvas-mirror]"), {subtree: true, childList: true, characterData: true, attributes: true})`)
	time.Sleep(260 * time.Millisecond)
	for i := 1; i <= 10; i++ {
		setNodesNow(t, ctx, []node{{Key: "a", Role: "button", Name: fmt.Sprintf("n%d", i), X: 0, Y: 0, W: 20, H: 20}})
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // the deferred frame lands
	seen := ev[[]string](t, ctx, `window.__seen`)
	distinct := slices.Compact(slices.Clone(seen))
	if len(distinct) > 2 || len(distinct) == 0 {
		t.Errorf("ten frames in 50 ms were published as %q, want at most 2 distinct states", distinct)
	}
	if final := ev[string](t, ctx, `document.querySelector("[data-webcanvas-mirror] button").textContent`); final != "n10" {
		t.Errorf("the DOM ended on %q, not on the last tree (n10): a deferred change was dropped", final)
	}
	ev[any](t, ctx, `window.__seen = []`)
	time.Sleep(260 * time.Millisecond)
	setNodesNow(t, ctx, []node{{Key: "a", Role: "button", Name: "n10", X: 0, Y: 0, W: 20, H: 20}})
	time.Sleep(100 * time.Millisecond)
	if n := ev[int](t, ctx, `window.__seen.length`); n != 0 {
		t.Errorf("an unchanged tree mutated the DOM %d times", n)
	}
}

// Close takes the mirror away with the listeners.
func TestMirrorCloseRemovesTheRoot(t *testing.T) {
	ctx := a11yPage(t)
	setNodes(t, ctx, sample)
	if !ev[bool](t, ctx, `!!document.querySelector("[data-webcanvas-mirror]")`) {
		t.Fatal("no mirror")
	}
	ev[any](t, ctx, `closeSurface()`)
	if ev[bool](t, ctx, `!!document.querySelector("[data-webcanvas-mirror]")`) {
		t.Error("the mirror root outlived Close")
	}
}

// The cost of the mirror on a large tree: Surface.Refresh around a 2,000-node
// change, median of rounds, scenarios in order within a round. Run with -v to
// read the table.
func TestMirrorCost(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	ctx := a11yPage(t)
	const n, rounds = 2000, 7
	scenarios := []string{"create", "unchanged", "one", "shift", "reverse", "dropHalf", "empty"}
	times := map[string][]float64{}
	for r := 0; r < rounds; r++ {
		for _, sc := range scenarios {
			time.Sleep(260 * time.Millisecond) // out of the throttle: this measures the work
			ms := ev[float64](t, ctx, fmt.Sprintf(`benchNodes(%q, %d)`, sc, n))
			if r > 0 { // round 0 warms the JIT and the DOM caches
				times[sc] = append(times[sc], ms)
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("\n| scenario (2,000 nodes) | median ms | min ms | max ms |\n|---|---:|---:|---:|\n")
	med := map[string]float64{}
	for _, sc := range scenarios {
		v := slices.Clone(times[sc])
		slices.Sort(v)
		med[sc] = v[len(v)/2]
		fmt.Fprintf(&sb, "| %s | %.2f | %.2f | %.2f |\n", sc, med[sc], v[0], v[len(v)-1])
	}
	t.Log(sb.String())
	// Loose bounds, only to catch an order-of-magnitude regression.
	if med["unchanged"] > 50 {
		t.Errorf("an unchanged 2,000-node frame costs %.1f ms: the signature check is not cheap", med["unchanged"])
	}
	if med["create"] > 2000 {
		t.Errorf("building 2,000 nodes costs %.0f ms", med["create"])
	}
}

// One key press activates exactly once. The scene here does not take Enter or
// Space, so the browser would click the focused native button AND the mirror
// would replay: the mirror cancels the keydown (UI Events: a canceled keydown
// dispatches no click) and replays once. When the scene does take the key, the
// mirror replays nothing.
func TestMirrorEnterAndSpaceActivateExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want string // what the scene hears
	}{{"Enter", kb.Enter, "down Enter"}, {"Space", " ", "char  "}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := a11yPage(t)
			s := slices.Clone(sample)
			s[0].Focused = true
			setNodes(t, ctx, sample)
			ev[any](t, ctx, `focusSurface()`)
			setNodesNow(t, ctx, s)
			if got := ev[string](t, ctx, `document.activeElement.textContent`); got != "Save" {
				t.Fatalf("focus is on %q, not the Save button", got)
			}

			ev[any](t, ctx, `window.__clicks = []; window.__keys = []; window.__consume = false`)
			if err := chromedp.Do(ctx, chromedp.KeyEvent(tc.key)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond) // a late click (Space clicks on keyup) would be here
			if got := ev[[]string](t, ctx, `window.__clicks`); !slices.Equal(got, []string{"click 50,35", "release 50,35"}) {
				t.Errorf("unconsumed %s activated as %q, want exactly one click and release", tc.name, got)
			}
			if got := ev[[]string](t, ctx, `window.__keys`); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("the scene heard %q, want %q", got, tc.want)
			}

			ev[any](t, ctx, `window.__clicks = []; window.__keys = []; window.__consume = true`)
			if err := chromedp.Do(ctx, chromedp.KeyEvent(tc.key)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			if got := ev[[]string](t, ctx, `window.__clicks`); len(got) != 0 {
				t.Errorf("a %s the scene consumed still clicked: %q", tc.name, got)
			}
			if got := ev[[]string](t, ctx, `window.__keys`); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("consumed: the scene heard %q, want %q", got, tc.want)
			}
		})
	}
}

// A tree above the cap builds a bounded DOM: the cap refuses the rest, keeps
// the focused node, and says the view is truncated.
func TestMirrorCapsALargeTree(t *testing.T) {
	ctx := a11yPage(t)
	const total, cap, focus = 12000, 5000, 11990
	time.Sleep(260 * time.Millisecond)
	ev[any](t, ctx, `capTree(`+strconv.Itoa(total)+`, `+strconv.Itoa(focus)+`)`)
	got := ev[map[string]any](t, ctx, `(() => { const r = document.querySelector("[data-webcanvas-mirror]");
		return {children: r.children.length, polite: r.children[0].textContent,
		        focused: r.querySelectorAll("button").length > 0 && [...r.querySelectorAll("button")].some(b => b.textContent === "Item 11990"),
		        last: !!r.querySelector('[aria-label="Item 11999"]'), first: [...r.querySelectorAll("button")].some(b => b.textContent === "Item 0"),
		        status: r.children[2].textContent}; })()`)
	if got["children"] != float64(cap+2) {
		t.Errorf("a %d-node tree built %v children, want %d (2 live regions + the cap)", total, got["children"], cap+2)
	}
	want := "Accessibility view truncated: showing 4999 of 12000 items"
	if got["status"] != want || got["polite"] != want {
		t.Errorf("status %q, polite region %q, want %q", got["status"], got["polite"], want)
	}
	if got["focused"] != true || got["first"] != true || got["last"] != false {
		t.Errorf("focused kept %v, first kept %v, last kept %v (want true, true, false)", got["focused"], got["first"], got["last"])
	}
	// Back under the cap, the truncation notice goes and everything shows.
	setNodes(t, ctx, sample)
	if n := ev[int](t, ctx, `document.querySelector("[data-webcanvas-mirror]").children.length`); n != 2+len(sample) {
		t.Errorf("after shrinking, %d children, want %d", n, 2+len(sample))
	}
}
