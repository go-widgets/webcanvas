// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math"
	"slices"
	"strconv"
)

// This file is the pure half of the accessibility mirror (#21, design (a)): the
// node type, the mapping from a node to an element, the diff between two
// frames, the tree signature, the publish throttle and the live-region text.
// mirror_js.go is the DOM glue that applies what these decide. Nothing here
// touches syscall/js, so all of it is tested natively at 100%.
//
// Why a mirror at all: a <canvas> is one opaque image to assistive technology
// (HTML-AAM §4.1.x, el-canvas: "descendants of the canvas element are mapped
// separately", the pixels say nothing). The mirror is a DOM subtree, a sibling
// of the canvas, that says in ARIA what the scene paints.

// A11yNode is one accessible element of a scene, in surface (device) pixels.
// It is the plain, dependency-free twin of toolkit.A11yNode: whoever owns the
// toolkit tree converts toolkit.WalkA11y into a slice of these, in reading
// order. Role names are WAI-ARIA 1.2 role names (toolkit.Role already uses
// them).
type A11yNode struct {
	// Key is stable across frames for the same widget (toolkit.A11yNode.Key:
	// a path of "@<id>" or "<Type>#<n>" segments). Two nodes of one frame must
	// not share a Key; the mirror keeps the first. An unchanged Key keeps the
	// same DOM element, so the browser keeps the same node and a screen
	// reader's virtual cursor is not reset on every frame.
	Key string
	// Role, Name and Value are the ARIA role, the accessible name and the
	// current value (a textbox's text, "checked", "pressed", ...). They come
	// from the app and may be untrusted: the mirror only ever puts them in
	// textContent and attribute values.
	Role, Name, Value string
	// X, Y, W, H are the node's bounds in surface pixels.
	X, Y, W, H int
	// Focused: the widget holds toolkit focus, so its element holds DOM focus.
	Focused bool
	// Disabled, ReadOnly and Multiline map to aria-disabled, aria-readonly and
	// aria-multiline.
	Disabled, ReadOnly, Multiline bool
	// HasRange gates Min, Max and Now (aria-valuemin, -valuemax, -valuenow).
	HasRange      bool
	Min, Max, Now float64
}

// A11yAware is implemented by a scene that publishes an accessibility tree.
// The mirror reads A11yNodes after every frame it draws.
type A11yAware interface {
	A11yNodes() []A11yNode
}

// elemSpec is what ariaFor says one node's element looks like: its tag, every
// attribute and its text. Values are raw, unescaped strings: they reach the DOM
// through setAttribute and textContent only, never through markup.
type elemSpec struct {
	Tag   string
	Attrs map[string]string
	Text  string
}

// WAI-ARIA 1.2 roles by how the mirror treats them.
var (
	// roleFromContent: the role's name comes from the element's content
	// (WAI-ARIA 1.2 §5.2.7.x, "Name From: contents"), so the Name is the text.
	roleFromContent = map[string]bool{
		"heading": true, "link": true, "tab": true, "menuitem": true, "option": true,
		"listitem": true, "cell": true, "gridcell": true, "columnheader": true,
		"rowheader": true, "treeitem": true, "tooltip": true, "paragraph": true,
		"caption": true,
	}
	// roleInteractive: the role takes focus. tabindex=-1, never 0: Tab is the
	// scene's (toolkit focus order), the browser must not move DOM focus on its
	// own.
	roleInteractive = map[string]bool{
		"button": true, "checkbox": true, "switch": true, "radio": true,
		"slider": true, "spinbutton": true, "textbox": true, "link": true,
		"tab": true, "menuitem": true, "option": true, "treeitem": true,
		"gridcell": true,
	}
	roleChecked = map[string]bool{"checkbox": true, "switch": true, "radio": true}
	roleRange   = map[string]bool{"slider": true, "progressbar": true, "meter": true, "spinbutton": true}
	roleLive    = map[string]bool{"status": true, "alert": true, "log": true}
)

// validRole reports whether r is safe to use as a role token: lowercase ASCII
// letters and hyphens. The role comes from the app, so anything else (spaces
// make a fallback list, quotes and markup are never wanted) is dropped.
func validRole(r string) bool {
	if r == "" {
		return false
	}
	for i := 0; i < len(r); i++ {
		if c := r[i]; (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

// formatNum writes an aria-value* number the shortest exact way.
func formatNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// css writes a pixel length in CSS pixels, to two decimals, trimmed.
func css(px int, scale float64) string {
	return strconv.FormatFloat(math.Round(float64(px)*scale*100)/100, 'f', -1, 64) + "px"
}

// styleFor positions a node's element over its widget. The string is built from
// numbers only.
func styleFor(n A11yNode, scale float64) string {
	return "position:absolute;left:" + css(n.X, scale) + ";top:" + css(n.Y, scale) +
		";width:" + css(n.W, scale) + ";height:" + css(n.H, scale) + ";margin:0;padding:0;border:0;overflow:hidden"
}

// checkedState maps a toolkit Value onto aria-checked.
func checkedState(v string) string {
	switch v {
	case "checked", "true":
		return "true"
	case "mixed":
		return "mixed"
	}
	return "false"
}

// ariaFor maps a node onto its element (WAI-ARIA 1.2 role definitions; the
// button gets a native <button>, so it has keyboard and click activation from
// the browser):
//
//   - button: <button type=button>, Name as text; aria-pressed when Value says
//     "pressed" or "selected".
//   - checkbox, switch, radio: role + aria-checked, Name as aria-label.
//   - slider, progressbar, meter, spinbutton: role + aria-valuemin/-max/-now
//     (when HasRange and finite) + aria-valuetext (the Value, when set).
//   - status, alert, log: a plain <div> holding the text. They are NOT live in
//     the tree: announcements go through the surface's two persistent live
//     regions (see [announce]), because a live region created together with its
//     content may not be announced, and a live node here would announce twice.
//   - text: <span>, Name as text.
//   - textbox: <div role=textbox> with the Value as text, aria-multiline and
//     aria-readonly when set. A focused textbox will later be replaced by the
//     text-input bridge's element (the hook is in mirror_js.go).
//   - any other valid role: <div role=...>, named by content for the roles
//     whose name comes from content, else by aria-label.
//
// Every node gets style (its bounds, in CSS pixels at scale), aria-disabled when
// disabled, and tabindex=-1 when its role takes focus.
func ariaFor(n A11yNode, scale float64) elemSpec {
	a := map[string]string{"style": styleFor(n, scale)}
	s := elemSpec{Tag: "div", Attrs: a}
	role := n.Role
	switch {
	case role == "button":
		s.Tag, s.Text = "button", n.Name
		a["type"] = "button"
		if n.Value == "pressed" || n.Value == "selected" {
			a["aria-pressed"] = "true"
		}
	case role == "text":
		s.Tag, s.Text = "span", n.Name
	case roleLive[role]:
		s.Text = n.Name
		if s.Text == "" {
			s.Text = n.Value
		}
	case role == "textbox":
		a["role"], s.Text = "textbox", n.Value
		if n.Name != "" {
			a["aria-label"] = n.Name
		}
		if n.Multiline {
			a["aria-multiline"] = "true"
		}
		if n.ReadOnly {
			a["aria-readonly"] = "true"
		}
	default:
		if validRole(role) {
			a["role"] = role
		}
		if roleFromContent[role] || !validRole(role) {
			s.Text = n.Name
		} else if n.Name != "" {
			a["aria-label"] = n.Name
		}
		if roleChecked[role] {
			a["aria-checked"] = checkedState(n.Value)
		}
		if roleRange[role] {
			if n.HasRange && finite(n.Min) && finite(n.Max) && finite(n.Now) {
				a["aria-valuemin"] = formatNum(n.Min)
				a["aria-valuemax"] = formatNum(n.Max)
				a["aria-valuenow"] = formatNum(n.Now)
			}
			if n.Value != "" {
				a["aria-valuetext"] = n.Value
			}
		}
	}
	if n.Disabled {
		a["aria-disabled"] = "true"
	}
	if roleInteractive[role] {
		a["tabindex"] = "-1"
	}
	return s
}

// opKind is one step of a [mirrorPlan].
type opKind int

const (
	opRemove opKind = iota // drop the element of Key
	opCreate               // build Key's element from Spec and insert it at Index
	opMove                 // move Key's existing element to Index
	opUpdate               // change Key's element: set Set, delete Del, retext if SetText
)

// op is one DOM step. Index counts nodes only (the glue adds the live regions
// that precede them).
type op struct {
	Kind  opKind
	Key   string
	Index int
	// Spec is the element to build (opCreate) or the new spec (opUpdate).
	Spec elemSpec
	// opUpdate: attributes to set, to delete, and the new text if SetText.
	Set     map[string]string
	Del     []string
	SetText bool
}

// frame is a published tree and the CSS-pixels-per-surface-pixel it was
// positioned at.
type frame struct {
	Nodes []A11yNode
	Scale float64
}

// dedupe keeps the first node of each Key.
func dedupe(ns []A11yNode) []A11yNode {
	seen := make(map[string]bool, len(ns))
	out := make([]A11yNode, 0, len(ns))
	for _, n := range ns {
		if !seen[n.Key] {
			seen[n.Key] = true
			out = append(out, n)
		}
	}
	return out
}

// mirrorPlan is the diff between the tree the DOM shows (prev) and the tree to
// show (next), by Key:
//
//  1. a key that vanished, or whose element tag changed (a button cannot become
//     a div in place), is removed;
//  2. then each next node, in order: a new key is created at its index; a key
//     in the wrong place is moved; a key whose element spec differs is updated
//     by attribute set/delete, so an unchanged key keeps its element.
//
// Applying the ops in order to prev's elements yields next's, in next's order.
// A tree with duplicate keys is reduced to the first node of each.
func mirrorPlan(prev, next frame) []op {
	prevNodes, nextNodes := dedupe(prev.Nodes), dedupe(next.Nodes)
	prevSpec := make(map[string]elemSpec, len(prevNodes))
	for _, n := range prevNodes {
		prevSpec[n.Key] = ariaFor(n, prev.Scale)
	}
	nextSpec := make(map[string]elemSpec, len(nextNodes))
	for _, n := range nextNodes {
		nextSpec[n.Key] = ariaFor(n, next.Scale)
	}
	var ops []op
	cur := make([]string, 0, len(prevNodes)) // the keys the DOM holds, in order
	for _, n := range prevNodes {
		ns, still := nextSpec[n.Key]
		if !still || ns.Tag != prevSpec[n.Key].Tag {
			ops = append(ops, op{Kind: opRemove, Key: n.Key})
			delete(prevSpec, n.Key)
			continue
		}
		cur = append(cur, n.Key)
	}
	for i, n := range nextNodes {
		spec := nextSpec[n.Key]
		old, existed := prevSpec[n.Key]
		if !existed {
			ops = append(ops, op{Kind: opCreate, Key: n.Key, Index: i, Spec: spec})
			cur = insertAt(cur, i, n.Key)
			continue
		}
		if cur[i] != n.Key {
			j := slices.Index(cur, n.Key) // present: it existed and was kept
			cur = insertAt(append(cur[:j], cur[j+1:]...), i, n.Key)
			ops = append(ops, op{Kind: opMove, Key: n.Key, Index: i})
		}
		if u, changed := specDiff(old, spec); changed {
			u.Key = n.Key
			ops = append(ops, u)
		}
	}
	return ops
}

func insertAt(s []string, i int, k string) []string {
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = k
	return s
}

// specDiff says how to turn old's element into new's (same tag).
func specDiff(old, new elemSpec) (op, bool) {
	u := op{Kind: opUpdate, Spec: new}
	for k, v := range new.Attrs {
		if ov, ok := old.Attrs[k]; !ok || ov != v {
			if u.Set == nil {
				u.Set = map[string]string{}
			}
			u.Set[k] = v
		}
	}
	for k := range old.Attrs {
		if _, ok := new.Attrs[k]; !ok {
			u.Del = append(u.Del, k)
		}
	}
	// Deleted attributes are applied in a fixed order, whatever the map says.
	slices.Sort(u.Del)
	u.SetText = old.Text != new.Text
	return u, u.Set != nil || u.Del != nil || u.SetText
}

// FNV-1a, 64-bit (the hash cocoa.a11yTreeSig uses).
const (
	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
)

func hashBytes(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}
	return h
}

func hashUint(h, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h ^= v & 0xff
		h *= fnvPrime
		v >>= 8
	}
	return h
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// treeSig is a 64-bit FNV-1a signature of everything in the nodes that reaches
// the DOM, and of the scale. Equal trees have equal signatures; a field
// change, an inserted node or a reorder changes it. A string is followed by a
// 0xff byte, which UTF-8 never contains, so ("ab","c") and ("a","bc") differ.
func treeSig(ns []A11yNode, scale float64) uint64 {
	h := hashUint(fnvOffset, math.Float64bits(scale))
	for _, n := range ns {
		for _, s := range [...]string{n.Key, n.Role, n.Name, n.Value} {
			h = hashBytes(h, s)
			h = hashUint(h, 0xff)
		}
		for _, v := range [...]int{n.X, n.Y, n.W, n.H} {
			h = hashUint(h, uint64(int64(v)))
		}
		h = hashUint(h, b2u(n.Focused)|b2u(n.Disabled)<<1|b2u(n.ReadOnly)<<2|b2u(n.Multiline)<<3|b2u(n.HasRange)<<4)
		for _, f := range [...]float64{n.Min, n.Max, n.Now} {
			h = hashUint(h, math.Float64bits(f))
		}
	}
	return h
}

// publishInterval is the least time, in milliseconds, between two mirror
// updates of a changing tree.
const publishInterval = 200

// action is what the throttle tells the glue to do with a frame's tree.
type action int

const (
	actSkip    action = iota // nothing to do: the DOM already shows it
	actPublish               // update the mirror now
	actDefer                 // too soon: look again after wait milliseconds
)

// throttle is the publish policy of the native bridges (cocoa.a11yShouldPublish):
// an unchanged tree is skipped, a changing one is published at most every
// [publishInterval] ms, and a resize forces. The difference from a plain
// rate limit is the trailing edge: a change refused as too soon is deferred,
// not dropped, so the DOM always catches up to the last tree.
type throttle struct {
	sig    uint64
	has    bool
	lastMS int64
}

// decide says what to do with a tree of signature sig seen at nowMS. wait is
// meaningful for actDefer only.
func (t *throttle) decide(sig uint64, force bool, nowMS int64) (a action, wait int64) {
	switch {
	case force:
		return actPublish, 0
	case t.has && sig == t.sig:
		return actSkip, 0
	case !t.has || nowMS-t.lastMS >= publishInterval:
		return actPublish, 0
	}
	return actDefer, publishInterval - (nowMS - t.lastMS)
}

// published records that the tree of signature sig was put in the DOM at nowMS.
func (t *throttle) published(sig uint64, nowMS int64) {
	t.sig, t.has, t.lastMS = sig, true, nowMS
}

// announcement is the text to put in the persistent live regions.
type announcement struct {
	Polite, Assertive string
}

// announce says what to announce when prev becomes next: the text of every
// status or log node (polite) and alert node (assertive; WAI-ARIA 1.2 aria-live:
// polite is "presented at the next graceful opportunity", assertive "presented
// the user immediately") that is new or whose text changed. A first tree
// (prev == nil) announces nothing: what is already on screen is not news. Texts
// of one kind are joined with a space, in tree order.
func announce(prev, next []A11yNode) announcement {
	if prev == nil {
		return announcement{}
	}
	before := make(map[string]string, len(prev))
	for _, n := range prev {
		if roleLive[n.Role] {
			before[n.Key] = liveText(n)
		}
	}
	var out announcement
	seen := make(map[string]bool, len(next))
	for _, n := range next {
		if !roleLive[n.Role] || seen[n.Key] {
			continue
		}
		seen[n.Key] = true
		t := liveText(n)
		if t == "" {
			continue
		}
		if old, ok := before[n.Key]; ok && old == t {
			continue
		}
		if n.Role == "alert" {
			out.Assertive = joinText(out.Assertive, t)
		} else {
			out.Polite = joinText(out.Polite, t)
		}
	}
	return out
}

func liveText(n A11yNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.Value
}

func joinText(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// The mirror root's style: the clip pattern for content that is visually hidden
// but still in the accessibility tree (WebAIM, "Invisible Content Just for
// Screen Reader Users": clip / clip-path: inset(50%) with overflow:hidden, never
// display:none or visibility:hidden, which "hide an element from every user"
// screen readers included; WAI-ARIA 1.2 defines hidden as "not rendered or
// explicitly hidden"). WebAIM's box is 1px; this root keeps the canvas' size
// instead, so that every child sits at its real coordinates and the browser
// reports real bounds (a screen reader's focus ring) -- clip-path: inset(50%)
// paints none of it. pointer-events:none lets the mouse through to the canvas.
const rootStyle = "position:absolute;overflow:hidden;clip:rect(0 0 0 0);clip-path:inset(50%);" +
	"white-space:pre;pointer-events:none;margin:0;padding:0;border:0"

// rootBox is the style of the root at the canvas' position and size, in CSS
// pixels.
func rootBox(left, top, w, h float64) string {
	f := func(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) + "px" }
	return rootStyle + ";left:" + f(left) + ";top:" + f(top) + ";width:" + f(w) + ";height:" + f(h)
}

// centre is the middle of a node, in surface pixels: where an activation from
// assistive technology is replayed as a click, as the native bridges do.
func centre(n A11yNode) (int, int) { return n.X + n.W/2, n.Y + n.H/2 }
