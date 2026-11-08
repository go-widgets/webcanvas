// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for the accessibility mirror: one canvas whose scene
// publishes the nodes JavaScript gives it. window.setNodes(json) replaces them
// and draws a frame; every press the scene hears is appended to window.__clicks.
package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"

	"github.com/go-widgets/webcanvas"
)

type scene struct{ nodes []webcanvas.A11yNode }

func (s *scene) Size() (int, int)      { return 400, 300 }
func (s *scene) Draw([]byte)           {}
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(k string) bool    { push("__keys", "char "+k); return consume() }
func (s *scene) KeyDown(k string) bool { push("__keys", "down "+k); return consume() }
func (s *scene) Click(x, y int) bool {
	push("__clicks", "click "+strconv.Itoa(x)+","+strconv.Itoa(y))
	return false
}
func (s *scene) Release(x, y int) bool {
	push("__clicks", "release "+strconv.Itoa(x)+","+strconv.Itoa(y))
	return false
}
func (s *scene) A11yNodes() []webcanvas.A11yNode { return s.nodes }

// consume is true when the page asked the scene to take every key
// (window.__consume), as a toolkit button that activates on Enter does.
func consume() bool { return js.Global().Get("__consume").Truthy() }

func push(name, v string) {
	l := js.Global().Get(name)
	if l.Type() != js.TypeObject {
		l = js.Global().Get("Array").New()
		js.Global().Set(name, l)
	}
	l.Call("push", v)
}

func main() {
	sc := &scene{}
	canvas := js.Global().Get("document").Call("getElementById", "screen")
	surf := webcanvas.Mount(canvas, sc)
	js.Global().Set("setNodes", js.FuncOf(func(_ js.Value, args []js.Value) any {
		var ns []webcanvas.A11yNode
		if err := json.Unmarshal([]byte(args[0].String()), &ns); err != nil {
			return err.Error()
		}
		sc.nodes = ns
		surf.Refresh()
		return nil
	}))
	// benchNodes(scenario, n) sets a generated tree of n nodes and returns the
	// milliseconds Surface.Refresh took (a one-pixel frame draw, the signature,
	// the plan and the DOM work). The tree is built before the clock starts.
	js.Global().Set("benchNodes", js.FuncOf(func(_ js.Value, args []js.Value) any {
		sc.nodes = benchTree(args[0].String(), args[1].Int())
		t0 := js.Global().Get("performance").Call("now").Float()
		surf.Refresh()
		return js.Global().Get("performance").Call("now").Float() - t0
	}))
	// capTree(n, focus) sets n nodes with node `focus` focused (-1 for none) and
	// draws a frame.
	js.Global().Set("capTree", js.FuncOf(func(_ js.Value, args []js.Value) any {
		sc.nodes = benchTree("create", args[0].Int())
		if f := args[1].Int(); f >= 0 {
			sc.nodes[f].Focused = true
		}
		surf.Refresh()
		return nil
	}))
	js.Global().Set("focusSurface", js.FuncOf(func(js.Value, []js.Value) any { surf.Focus(); return nil }))
	js.Global().Set("closeSurface", js.FuncOf(func(js.Value, []js.Value) any { surf.Close(); return nil }))
	js.Global().Set("a11yReady", true)
	select {}
}

// benchTree is the n-node tree of a scenario: a mix of roles on a grid.
func benchTree(scenario string, n int) []webcanvas.A11yNode {
	roles := []string{"button", "checkbox", "text", "slider", "textbox"}
	ns := make([]webcanvas.A11yNode, 0, n)
	for i := 0; i < n; i++ {
		x := (i % 50) * 8
		if scenario == "shift" {
			x++
		}
		node := webcanvas.A11yNode{Key: "k" + strconv.Itoa(i), Role: roles[i%len(roles)], Name: "Item " + strconv.Itoa(i),
			X: x, Y: (i / 50) * 8, W: 8, H: 8}
		switch node.Role {
		case "slider":
			node.HasRange, node.Max, node.Now = true, 100, float64(i%100)
		case "textbox":
			node.Value, node.Multiline = "line one\nline two", true
		}
		ns = append(ns, node)
	}
	switch scenario {
	case "empty":
		return nil
	case "one":
		ns[n/2].Name = "changed"
	case "reverse":
		for i, j := 0, len(ns)-1; i < j; i, j = i+1, j-1 {
			ns[i], ns[j] = ns[j], ns[i]
		}
	case "dropHalf":
		kept := ns[:0]
		for i, v := range ns {
			if i%2 == 0 {
				kept = append(kept, v)
			}
		}
		ns = kept
	}
	return ns
}
