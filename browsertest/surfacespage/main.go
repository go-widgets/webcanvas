// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for Mount: two surfaces among prose. "a" consumes the
// wheel, "b" does not. Every key a scene hears is appended to window.__log[name].
package main

import (
	"syscall/js"

	"github.com/go-widgets/webcanvas"
)

type scene struct {
	name    string
	consume bool
}

func (s *scene) log(kind, key string) bool {
	l := js.Global().Get("__log")
	if l.Type() != js.TypeObject {
		l = js.Global().Get("Object").New()
		js.Global().Set("__log", l)
	}
	if l.Get(s.name).Type() != js.TypeObject {
		l.Set(s.name, js.Global().Get("Array").New())
	}
	l.Get(s.name).Call("push", kind+" "+key)
	return false
}

func (s *scene) Size() (int, int) { return 100, 100 }
func (s *scene) Draw(buf []byte) {
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = 0, 0, 255, 255
	}
}
func (s *scene) Click(int, int) bool   { return false }
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Release(int, int) bool { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(key string) bool  { return s.log("char", key) }
func (s *scene) KeyDown(k string) bool { return s.log("down", k) }

// Scroll is a webcanvas.ScrollConsumer: it consumes the wheel only if told to.
func (s *scene) Scroll(x, y, dx, dy int) (repaint, consumed bool) {
	s.log("wheel", "")
	return false, s.consume
}

func main() {
	surfaces := map[string]*webcanvas.Surface{}
	for _, sc := range []*scene{{name: "a", consume: true}, {name: "b"}} {
		canvas := js.Global().Get("document").Call("getElementById", sc.name)
		surfaces[sc.name] = webcanvas.Mount(canvas, sc)
	}
	js.Global().Set("closeSurface", js.FuncOf(func(_ js.Value, args []js.Value) any {
		surfaces[args[0].String()].Close()
		return nil
	}))
	js.Global().Set("surfacesMounted", true)
	select {}
}
