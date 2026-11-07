// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test for RunWith: a presenter that takes the canvas paints
// it green through a context of its own, and the scene's Draw, which would
// paint blue, is never called. window.__draws counts Draw calls and
// window.__frames the presenter's frames.
package main

import (
	"syscall/js"

	"github.com/go-widgets/webcanvas"
)

type scene struct{}

func (scene) Size() (int, int) { return 8, 8 }
func (scene) Draw(buf []byte) {
	js.Global().Set("__draws", js.Global().Get("__draws").Int()+1)
	for i := 0; i < len(buf); i += 4 {
		buf[i+2], buf[i+3] = 255, 255
	}
}
func (scene) Click(int, int) bool   { return false }
func (scene) Move(int, int) bool    { return false }
func (scene) Release(int, int) bool { return false }
func (scene) Context(int, int) bool { return false }
func (scene) Char(string) bool      { return false }
func (scene) KeyDown(string) bool   { return false }

type presenter struct{ ctx js.Value }

func (p *presenter) Attach(canvas js.Value, _ webcanvas.App, w, h int) bool {
	if canvas.Get("width").Int() != w || canvas.Get("height").Int() != h {
		return false // Attach must be handed a canvas already sized
	}
	p.ctx = canvas.Call("getContext", "2d")
	return true
}

func (p *presenter) Frame(_ webcanvas.App, w, h int) {
	js.Global().Set("__frames", js.Global().Get("__frames").Int()+1)
	p.ctx.Set("fillStyle", "#00ff00")
	p.ctx.Call("fillRect", 0, 0, w, h)
}

func main() {
	js.Global().Set("__draws", 0)
	js.Global().Set("__frames", 0)
	webcanvas.RunWith("screen", scene{}, &presenter{})
}
