// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestValidRole(t *testing.T) {
	for r, want := range map[string]bool{
		"": false, "button": true, "menuitem": true, "tree-item": true,
		"Button": false, "a b": false, "a\"b": false, "<img>": false, "x1": false,
	} {
		if got := validRole(r); got != want {
			t.Errorf("validRole(%q) = %v, want %v", r, got, want)
		}
	}
}

func TestFormatting(t *testing.T) {
	for _, c := range []struct {
		px    int
		scale float64
		want  string
	}{{0, 1, "0px"}, {10, 1, "10px"}, {10, 0.5, "5px"}, {7, 0.5, "3.5px"}, {1, 1.0 / 3, "0.33px"}, {-4, 1, "-4px"}} {
		if got := css(c.px, c.scale); got != c.want {
			t.Errorf("css(%d, %v) = %q, want %q", c.px, c.scale, got, c.want)
		}
	}
	if formatNum(0.5) != "0.5" || formatNum(100) != "100" {
		t.Error("formatNum")
	}
	if !finite(1) || finite(math.NaN()) || finite(math.Inf(-1)) {
		t.Error("finite")
	}
	for v, want := range map[string]string{"checked": "true", "true": "true", "mixed": "mixed", "": "false", "x": "false"} {
		if got := checkedState(v); got != want {
			t.Errorf("checkedState(%q) = %q, want %q", v, got, want)
		}
	}
	if x, y := centre(A11yNode{X: 10, Y: 20, W: 7, H: 4}); x != 13 || y != 22 {
		t.Errorf("centre = %d,%d", x, y)
	}
}

const style10 = "position:absolute;left:10px;top:20px;width:30px;height:40px;margin:0;padding:0;border:0;overflow:hidden"

func TestAriaFor(t *testing.T) {
	box := A11yNode{X: 10, Y: 20, W: 30, H: 40}
	with := func(f func(*A11yNode)) A11yNode { n := box; f(&n); return n }
	rng := func(f func(*A11yNode)) A11yNode {
		return with(func(n *A11yNode) { n.HasRange, n.Min, n.Max, n.Now = true, 0, 10, 2.5; f(n) })
	}
	tests := []struct {
		name string
		node A11yNode
		want elemSpec
	}{
		{"button", with(func(n *A11yNode) { n.Role, n.Name = "button", "Save" }),
			elemSpec{"button", map[string]string{"type": "button", "tabindex": "-1"}, "Save"}},
		{"button pressed", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "button", "B", "pressed" }),
			elemSpec{"button", map[string]string{"type": "button", "tabindex": "-1", "aria-pressed": "true"}, "B"}},
		{"button selected", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "button", "B", "selected" }),
			elemSpec{"button", map[string]string{"type": "button", "tabindex": "-1", "aria-pressed": "true"}, "B"}},
		{"button disabled", with(func(n *A11yNode) { n.Role, n.Name, n.Disabled = "button", "B", true }),
			elemSpec{"button", map[string]string{"type": "button", "tabindex": "-1", "aria-disabled": "true"}, "B"}},
		{"text", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "text", "Hello", "ignored" }),
			elemSpec{"span", map[string]string{}, "Hello"}},
		{"checkbox on", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "checkbox", "Wrap", "checked" }),
			elemSpec{"div", map[string]string{"role": "checkbox", "aria-label": "Wrap", "aria-checked": "true", "tabindex": "-1"}, ""}},
		{"checkbox off", with(func(n *A11yNode) { n.Role, n.Name = "checkbox", "Wrap" }),
			elemSpec{"div", map[string]string{"role": "checkbox", "aria-label": "Wrap", "aria-checked": "false", "tabindex": "-1"}, ""}},
		{"switch mixed", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "switch", "S", "mixed" }),
			elemSpec{"div", map[string]string{"role": "switch", "aria-label": "S", "aria-checked": "mixed", "tabindex": "-1"}, ""}},
		{"radio", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "radio", "R", "checked" }),
			elemSpec{"div", map[string]string{"role": "radio", "aria-label": "R", "aria-checked": "true", "tabindex": "-1"}, ""}},
		{"slider", rng(func(n *A11yNode) { n.Role, n.Name, n.Value = "slider", "Vol", "2.5 of 10" }),
			elemSpec{"div", map[string]string{"role": "slider", "aria-label": "Vol", "aria-valuemin": "0", "aria-valuemax": "10",
				"aria-valuenow": "2.5", "aria-valuetext": "2.5 of 10", "tabindex": "-1"}, ""}},
		{"progressbar", rng(func(n *A11yNode) { n.Role, n.Name = "progressbar", "Load" }),
			elemSpec{"div", map[string]string{"role": "progressbar", "aria-label": "Load", "aria-valuemin": "0", "aria-valuemax": "10", "aria-valuenow": "2.5"}, ""}},
		{"meter", rng(func(n *A11yNode) { n.Role = "meter" }),
			elemSpec{"div", map[string]string{"role": "meter", "aria-valuemin": "0", "aria-valuemax": "10", "aria-valuenow": "2.5"}, ""}},
		{"spinbutton", rng(func(n *A11yNode) { n.Role, n.Name = "spinbutton", "Qty" }),
			elemSpec{"div", map[string]string{"role": "spinbutton", "aria-label": "Qty", "aria-valuemin": "0", "aria-valuemax": "10", "aria-valuenow": "2.5", "tabindex": "-1"}, ""}},
		{"slider without range", with(func(n *A11yNode) { n.Role, n.Value = "slider", "loud" }),
			elemSpec{"div", map[string]string{"role": "slider", "aria-valuetext": "loud", "tabindex": "-1"}, ""}},
		{"slider NaN range", rng(func(n *A11yNode) { n.Role, n.Now = "slider", math.NaN() }),
			elemSpec{"div", map[string]string{"role": "slider", "tabindex": "-1"}, ""}},
		{"slider Inf min", rng(func(n *A11yNode) { n.Role, n.Min = "slider", math.Inf(-1) }),
			elemSpec{"div", map[string]string{"role": "slider", "tabindex": "-1"}, ""}},
		{"slider Inf max", rng(func(n *A11yNode) { n.Role, n.Max = "slider", math.Inf(1) }),
			elemSpec{"div", map[string]string{"role": "slider", "tabindex": "-1"}, ""}},
		{"status", with(func(n *A11yNode) { n.Role, n.Name = "status", "Saved" }),
			elemSpec{"div", map[string]string{}, "Saved"}},
		{"alert value", with(func(n *A11yNode) { n.Role, n.Value = "alert", "Disk full" }),
			elemSpec{"div", map[string]string{}, "Disk full"}},
		{"log", with(func(n *A11yNode) { n.Role, n.Name = "log", "line" }),
			elemSpec{"div", map[string]string{}, "line"}},
		{"textbox single", with(func(n *A11yNode) { n.Role, n.Name, n.Value = "textbox", "Name", "Ada" }),
			elemSpec{"div", map[string]string{"role": "textbox", "aria-label": "Name", "tabindex": "-1"}, "Ada"}},
		{"textbox code", with(func(n *A11yNode) {
			n.Role, n.Name, n.Value, n.Multiline, n.ReadOnly = "textbox", "Go cell 3", "x := 1\ny := 2", true, true
		}),
			elemSpec{"div", map[string]string{"role": "textbox", "aria-label": "Go cell 3", "aria-multiline": "true", "aria-readonly": "true", "tabindex": "-1"}, "x := 1\ny := 2"}},
		{"textbox unnamed", with(func(n *A11yNode) { n.Role = "textbox" }),
			elemSpec{"div", map[string]string{"role": "textbox", "tabindex": "-1"}, ""}},
		{"content role", with(func(n *A11yNode) { n.Role, n.Name = "tab", "General" }),
			elemSpec{"div", map[string]string{"role": "tab", "tabindex": "-1"}, "General"}},
		{"label role", with(func(n *A11yNode) { n.Role, n.Name = "group", "Options" }),
			elemSpec{"div", map[string]string{"role": "group", "aria-label": "Options"}, ""}},
		{"unnamed label role", with(func(n *A11yNode) { n.Role = "group" }),
			elemSpec{"div", map[string]string{"role": "group"}, ""}},
		{"invalid role", with(func(n *A11yNode) { n.Role, n.Name = `x" onclick="boom`, "Name" }),
			elemSpec{"div", map[string]string{}, "Name"}},
		{"empty role", with(func(n *A11yNode) { n.Name = "Name" }),
			elemSpec{"div", map[string]string{}, "Name"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ariaFor(tc.node, 1)
			tc.want.Attrs["style"] = style10
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ariaFor = %#v\nwant     %#v", got, tc.want)
			}
		})
	}
}

// Markup in a name or value is data: it is returned raw, to be set by
// textContent or setAttribute, and never reaches a tag or an attribute name.
func TestAriaForKeepsMarkupAsData(t *testing.T) {
	evil := `<img src=x onerror="alert(1)">"'`
	for _, role := range []string{"button", "text", "textbox", "checkbox", "group", "tab", "status", "x<y"} {
		s := ariaFor(A11yNode{Role: role, Name: evil, Value: evil}, 1)
		if s.Tag != "div" && s.Tag != "span" && s.Tag != "button" {
			t.Errorf("%s: tag %q", role, s.Tag)
		}
		for k := range s.Attrs {
			if k != "style" && k != "role" && k != "type" && k != "tabindex" && k[:5] != "aria-" {
				t.Errorf("%s: attribute name %q", role, k)
			}
		}
		if r, ok := s.Attrs["role"]; ok && !validRole(r) {
			t.Errorf("%s: role %q", role, r)
		}
	}
}

func nodes(keys ...string) []A11yNode {
	var out []A11yNode
	for i, k := range keys {
		out = append(out, A11yNode{Key: k, Role: "button", Name: k, X: i * 10, W: 10, H: 10})
	}
	return out
}

// applyOps replays a plan on a list of keys, the way the glue replays it on the
// root's children, and returns the keys left in order.
func applyOps(t *testing.T, cur []string, ops []op) []string {
	t.Helper()
	cur = slices.Clone(cur)
	for _, o := range ops {
		switch o.Kind {
		case opRemove:
			i := slices.Index(cur, o.Key)
			if i < 0 {
				t.Fatalf("remove of absent %q", o.Key)
			}
			cur = slices.Delete(cur, i, i+1)
		case opCreate:
			if slices.Contains(cur, o.Key) || o.Index > len(cur) {
				t.Fatalf("bad create %q at %d in %v", o.Key, o.Index, cur)
			}
			cur = slices.Insert(cur, o.Index, o.Key)
		case opMove:
			i := slices.Index(cur, o.Key)
			if i < 0 {
				t.Fatalf("move of absent %q", o.Key)
			}
			cur = slices.Delete(cur, i, i+1)
			cur = slices.Insert(cur, o.Index, o.Key)
		case opUpdate:
			if !slices.Contains(cur, o.Key) {
				t.Fatalf("update of absent %q", o.Key)
			}
		}
	}
	return cur
}

func keysOf(ns []A11yNode) []string {
	var k []string
	for _, n := range dedupe(ns) {
		k = append(k, n.Key)
	}
	return k
}

func TestMirrorPlan(t *testing.T) {
	one := func(ns []A11yNode) frame { return frame{ns, 1} }
	t.Run("first frame creates in order", func(t *testing.T) {
		ops := mirrorPlan(frame{}, one(nodes("a", "b")))
		if len(ops) != 2 || ops[0].Kind != opCreate || ops[0].Index != 0 || ops[1].Kind != opCreate || ops[1].Index != 1 || ops[1].Key != "b" {
			t.Fatalf("%+v", ops)
		}
		if ops[0].Spec.Tag != "button" || ops[0].Spec.Text != "a" {
			t.Fatalf("spec %+v", ops[0].Spec)
		}
	})
	t.Run("unchanged tree: no ops", func(t *testing.T) {
		if ops := mirrorPlan(one(nodes("a", "b", "c")), one(nodes("a", "b", "c"))); len(ops) != 0 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("empty to empty", func(t *testing.T) {
		if ops := mirrorPlan(frame{}, frame{}); len(ops) != 0 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("text and attribute changes update in place", func(t *testing.T) {
		prev := nodes("a", "b")
		next := nodes("a", "b")
		next[1].Name = "B!"
		next[1].Disabled = true
		next[0].X = 50
		ops := mirrorPlan(one(prev), one(next))
		if len(ops) != 2 || ops[0].Kind != opUpdate || ops[1].Kind != opUpdate {
			t.Fatalf("%+v", ops)
		}
		if ops[0].Key != "a" || ops[0].SetText || ops[0].Set["style"] == "" || len(ops[0].Set) != 1 {
			t.Fatalf("geometry-only update: %+v", ops[0])
		}
		if ops[1].Key != "b" || !ops[1].SetText || ops[1].Set["aria-disabled"] != "true" {
			t.Fatalf("%+v", ops[1])
		}
	})
	t.Run("attribute removal", func(t *testing.T) {
		prev := nodes("a")
		prev[0].Disabled = true
		prev[0].Value = "pressed"
		ops := mirrorPlan(one(prev), one(nodes("a")))
		if len(ops) != 1 || !reflect.DeepEqual(ops[0].Del, []string{"aria-disabled", "aria-pressed"}) || ops[0].Set != nil || ops[0].SetText {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("removal", func(t *testing.T) {
		ops := mirrorPlan(one(nodes("a", "b", "c")), one(nodes("a", "c")))
		if len(ops) != 2 || ops[0].Kind != opRemove || ops[0].Key != "b" || ops[1].Kind != opUpdate || ops[1].Key != "c" {
			// c moved from x=20 to x=10: its geometry is updated, its element kept.
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("insertion keeps the others", func(t *testing.T) {
		prev, next := nodes("a", "c"), nodes("a", "b", "c")
		next[0], next[2] = prev[0], prev[1]
		ops := mirrorPlan(one(prev), one(next))
		if len(ops) != 1 || ops[0].Kind != opCreate || ops[0].Key != "b" || ops[0].Index != 1 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("reorder moves", func(t *testing.T) {
		a, b, c := nodes("a")[0], nodes("b")[0], nodes("c")[0]
		ops := mirrorPlan(one([]A11yNode{a, b, c}), one([]A11yNode{c, a, b}))
		if len(ops) != 1 || ops[0].Kind != opMove || ops[0].Key != "c" || ops[0].Index != 0 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("tag change recreates", func(t *testing.T) {
		prev := nodes("a", "b")
		next := nodes("a", "b")
		next[0].Role = "checkbox"
		next[1] = prev[1]
		ops := mirrorPlan(one(prev), one(next))
		if len(ops) != 2 || ops[0].Kind != opRemove || ops[0].Key != "a" || ops[1].Kind != opCreate || ops[1].Key != "a" || ops[1].Index != 0 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("duplicate keys keep the first", func(t *testing.T) {
		ns := nodes("a", "a", "b")
		ops := mirrorPlan(frame{}, one(ns))
		if len(ops) != 2 || ops[0].Key != "a" || ops[1].Key != "b" {
			t.Fatalf("%+v", ops)
		}
		if ops := mirrorPlan(one(ns), one(ns)); len(ops) != 0 {
			t.Fatalf("%+v", ops)
		}
	})
	t.Run("a new scale restyles every kept node", func(t *testing.T) {
		ops := mirrorPlan(frame{nodes("a", "b"), 1}, frame{nodes("a", "b"), 0.5})
		if len(ops) != 2 || ops[0].Kind != opUpdate || ops[1].Kind != opUpdate || ops[0].SetText {
			t.Fatalf("%+v", ops)
		}
	})
}

// Whatever the two trees are, applying the plan to the old order yields the new
// one; and a key present in both trees with the same tag is never created or
// removed, which is what keeps its DOM element.
func TestMirrorPlanProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	roles := []string{"button", "text", "checkbox", "textbox"}
	pick := func() []A11yNode {
		var out []A11yNode
		for i := 0; i < rng.Intn(12); i++ {
			out = append(out, A11yNode{Key: "k" + strconv.Itoa(rng.Intn(9)), Role: roles[rng.Intn(len(roles))], Name: string(rune('a' + rng.Intn(3))), X: rng.Intn(3) * 5})
		}
		return out
	}
	for range 2000 {
		prev, next := pick(), pick()
		ops := mirrorPlan(frame{prev, 1}, frame{next, 1})
		if got, want := applyOps(t, keysOf(prev), ops), keysOf(next); !slices.Equal(got, want) {
			t.Fatalf("prev %v next %v: got %v", keysOf(prev), want, got)
		}
		tagOf := map[string]string{}
		for _, n := range dedupe(prev) {
			tagOf[n.Key] = ariaFor(n, 1).Tag
		}
		for _, n := range dedupe(next) {
			if tag, both := tagOf[n.Key]; both && tag == ariaFor(n, 1).Tag {
				for _, o := range ops {
					if o.Key == n.Key && (o.Kind == opCreate || o.Kind == opRemove) {
						t.Fatalf("key %q survives with the same tag yet got %v", n.Key, o.Kind)
					}
				}
			}
		}
	}
}

func TestTreeSig(t *testing.T) {
	base := A11yNode{Key: "k", Role: "slider", Name: "n", Value: "v", X: 1, Y: 2, W: 3, H: 4, HasRange: true, Min: 0, Max: 1, Now: 0.5}
	sig := treeSig([]A11yNode{base}, 1)
	if sig != treeSig([]A11yNode{base}, 1) {
		t.Fatal("not deterministic")
	}
	changes := map[string]func(*A11yNode){
		"key": func(n *A11yNode) { n.Key = "j" }, "role": func(n *A11yNode) { n.Role = "meter" },
		"name": func(n *A11yNode) { n.Name = "m" }, "value": func(n *A11yNode) { n.Value = "w" },
		"x": func(n *A11yNode) { n.X++ }, "y": func(n *A11yNode) { n.Y++ }, "w": func(n *A11yNode) { n.W++ }, "h": func(n *A11yNode) { n.H++ },
		"focused": func(n *A11yNode) { n.Focused = true }, "disabled": func(n *A11yNode) { n.Disabled = true },
		"readonly": func(n *A11yNode) { n.ReadOnly = true }, "multiline": func(n *A11yNode) { n.Multiline = true },
		"hasrange": func(n *A11yNode) { n.HasRange = false }, "min": func(n *A11yNode) { n.Min = -1 },
		"max": func(n *A11yNode) { n.Max = 2 }, "now": func(n *A11yNode) { n.Now = 0.6 },
		"negative x": func(n *A11yNode) { n.X = -1 },
	}
	seen := map[uint64]string{sig: "base"}
	for name, f := range changes {
		n := base
		f(&n)
		s := treeSig([]A11yNode{n}, 1)
		if prev, dup := seen[s]; dup {
			t.Errorf("%s collides with %s", name, prev)
		}
		seen[s] = name
	}
	if treeSig([]A11yNode{base}, 2) == sig {
		t.Error("scale ignored")
	}
	if treeSig(nil, 1) == sig || treeSig([]A11yNode{base, base}, 1) == sig {
		t.Error("length ignored")
	}
	a, b := A11yNode{Key: "ab", Role: "c"}, A11yNode{Key: "a", Role: "bc"}
	if treeSig([]A11yNode{a}, 1) == treeSig([]A11yNode{b}, 1) {
		t.Error("string boundary ignored")
	}
	x, y := A11yNode{Key: "x"}, A11yNode{Key: "y"}
	if treeSig([]A11yNode{x, y}, 1) == treeSig([]A11yNode{y, x}, 1) {
		t.Error("order ignored")
	}
}

func TestThrottle(t *testing.T) {
	var th throttle
	check := func(name string, sig uint64, force bool, now int64, wantA action, wantWait int64) {
		t.Helper()
		if a, w := th.decide(sig, force, now); a != wantA || w != wantWait {
			t.Errorf("%s: decide(%d, %v, %d) = %v, %d; want %v, %d", name, sig, force, now, a, w, wantA, wantWait)
		}
	}
	check("first tree publishes", 1, false, 1000, actPublish, 0)
	th.published(1, 1000)
	check("unchanged skips", 1, false, 1001, actSkip, 0)
	check("unchanged skips even much later", 1, false, 99999, actSkip, 0)
	check("change too soon defers", 2, false, 1050, actDefer, 150)
	check("change at the limit publishes", 2, false, 1200, actPublish, 0)
	check("change after the limit publishes", 2, false, 5000, actPublish, 0)
	check("force publishes at once", 2, true, 1001, actPublish, 0)
	check("force publishes an unchanged tree", 1, true, 1001, actPublish, 0)
	check("a change reverted while deferred skips", 1, false, 1100, actSkip, 0)
	th.published(2, 1200)
	check("after a publish the clock restarts", 3, false, 1300, actDefer, 100)
	check("one ms short", 3, false, 1399, actDefer, 1)
	check("exactly the interval", 3, false, 1400, actPublish, 0)
}

func TestAnnounce(t *testing.T) {
	st := func(key, role, name string) A11yNode { return A11yNode{Key: key, Role: role, Name: name} }
	tests := []struct {
		name       string
		prev, next []A11yNode
		want       announcement
	}{
		{"first tree is not news", nil, []A11yNode{st("s", "status", "Ready")}, announcement{}},
		{"unchanged", []A11yNode{st("s", "status", "Ready")}, []A11yNode{st("s", "status", "Ready")}, announcement{}},
		{"changed status is polite", []A11yNode{st("s", "status", "Ready")}, []A11yNode{st("s", "status", "Saved")}, announcement{Polite: "Saved"}},
		{"new log is polite", []A11yNode{}, []A11yNode{st("l", "log", "line 1")}, announcement{Polite: "line 1"}},
		{"new alert is assertive", []A11yNode{}, []A11yNode{st("a", "alert", "Disk full")}, announcement{Assertive: "Disk full"}},
		{"both, joined in order", []A11yNode{},
			[]A11yNode{st("s", "status", "one"), st("a", "alert", "two"), st("l", "log", "three")},
			announcement{Polite: "one three", Assertive: "two"}},
		{"the value is the text of an unnamed one", []A11yNode{}, []A11yNode{{Key: "s", Role: "status", Value: "v"}}, announcement{Polite: "v"}},
		{"emptied text says nothing", []A11yNode{st("s", "status", "x")}, []A11yNode{st("s", "status", "")}, announcement{}},
		{"other roles are silent", []A11yNode{}, []A11yNode{st("b", "button", "Go")}, announcement{}},
		{"a duplicate key announces once", []A11yNode{}, []A11yNode{st("s", "status", "x"), st("s", "status", "y")}, announcement{Polite: "x"}},
		{"a button that became a status is new", []A11yNode{st("s", "button", "x")}, []A11yNode{st("s", "status", "x")}, announcement{Polite: "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := announce(tc.prev, tc.next); got != tc.want {
				t.Errorf("announce = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The mirror is hidden by clipping, never by what removes it from the
// accessibility tree.
func TestRootStyle(t *testing.T) {
	s := rootBox(10.5, 20, 300, 150.256)
	for _, bad := range []string{"display:none", "visibility:hidden", "opacity"} {
		if containsStr(s, bad) {
			t.Errorf("root style has %q: %s", bad, s)
		}
	}
	for _, want := range []string{"clip-path:inset(50%)", "overflow:hidden", "pointer-events:none", "position:absolute",
		"left:10.5px", "top:20px", "width:300px", "height:150.26px"} {
		if !containsStr(s, want) {
			t.Errorf("root style lacks %q: %s", want, s)
		}
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
