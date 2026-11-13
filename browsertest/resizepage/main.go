// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for Surface.Resize: two surfaces whose scenes are
// Resizers. Each counts the Resize calls it gets, in window.__resizes[name] as
// the [w, h] of the last one and window.__resizeCount[name].
package main

import (
	"syscall/js"

	"github.com/go-widgets/webcanvas"
)

type scene struct {
	name string
	w, h int
}

func (s *scene) Size() (int, int) { return 50, 50 }
func (s *scene) Draw(buf []byte) {
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = 0, 0, 255, 255
	}
}
func (s *scene) Click(int, int) bool   { return false }
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Release(int, int) bool { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(string) bool      { return false }
func (s *scene) KeyDown(string) bool   { return false }

func (s *scene) Resize(w, h int) (int, int) {
	s.w, s.h = w, h
	g := js.Global()
	for _, k := range []string{"__resizes", "__resizeCount"} {
		if g.Get(k).Type() != js.TypeObject {
			g.Set(k, g.Get("Object").New())
		}
	}
	g.Get("__resizes").Set(s.name, []any{w, h})
	n := 0
	if v := g.Get("__resizeCount").Get(s.name); v.Type() == js.TypeNumber {
		n = v.Int()
	}
	g.Get("__resizeCount").Set(s.name, n+1)
	return w, h
}

func main() {
	surfaces := map[string]*webcanvas.Surface{}
	for _, name := range []string{"a", "b"} {
		canvas := js.Global().Get("document").Call("getElementById", name)
		surfaces[name] = webcanvas.Mount(canvas, &scene{name: name})
	}
	js.Global().Set("resizeSurface", js.FuncOf(func(_ js.Value, a []js.Value) any {
		surfaces[a[0].String()].Resize()
		return nil
	}))
	js.Global().Set("closeSurface", js.FuncOf(func(_ js.Value, a []js.Value) any {
		surfaces[a[0].String()].Close()
		return nil
	}))
	js.Global().Set("resizeReady", true)
	select {}
}
