// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test: a scene that turns red 100 ms after the first frame,
// from a goroutine, with no input event -- only RepaintWith can show it.
package main

import (
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/go-widgets/webcanvas"
)

type scene struct {
	red     atomic.Bool
	request func()
	// layers: a closed ImageBitmap (drawImage throws on it), then a green
	// 2×2 one scaled into the bottom-right 4×4 corner, once both exist.
	layers []webcanvas.Layer
}

func (s *scene) Layers() []webcanvas.Layer { return s.layers }

// bitmaps makes the two ImageBitmaps, then asks for a frame.
func (s *scene) bitmaps() {
	green := js.Global().Get("Uint8ClampedArray").New(16)
	for i := 0; i < 16; i += 4 {
		green.SetIndex(i+1, 255)
		green.SetIndex(i+3, 255)
	}
	data := js.Global().Get("ImageData").New(green, 2, 2)
	var done js.Func
	done = js.FuncOf(func(_ js.Value, args []js.Value) any {
		pair := args[0]
		closed := pair.Index(0)
		closed.Call("close")
		s.layers = []webcanvas.Layer{
			{Image: webcanvas.NewImage(closed), X: 0, Y: 0, W: 8, H: 8},
			{Image: webcanvas.NewImage(pair.Index(1)), X: 4, Y: 4, W: 4, H: 4, Alpha: 1},
		}
		if s.request != nil {
			s.request()
		}
		done.Release()
		return nil
	})
	js.Global().Get("Promise").Call("all", []any{
		js.Global().Call("createImageBitmap", data),
		js.Global().Call("createImageBitmap", data),
	}).Call("then", done)
}

func (s *scene) Size() (int, int) { return 8, 8 }

func (s *scene) Draw(buf []byte) {
	r, b := byte(0), byte(255)
	if s.red.Load() {
		r, b = 255, 0
	}
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = r, 0, b, 255
	}
}

func (s *scene) RepaintWith(request func()) { s.request = request }

func (s *scene) Click(int, int) bool   { return false }
func (s *scene) Move(int, int) bool    { return false }
func (s *scene) Release(int, int) bool { return false }
func (s *scene) Context(int, int) bool { return false }
func (s *scene) Char(string) bool      { return false }
func (s *scene) KeyDown(string) bool   { return false }

func main() {
	s := &scene{}
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.red.Store(true)
		if s.request != nil {
			s.request()
		}
	}()
	s.bitmaps()
	webcanvas.Run("screen", s)
}
