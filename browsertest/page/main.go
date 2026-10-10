// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The page under test: a scene that turns red 100 ms after the first frame,
// from a goroutine, with no input event -- only RepaintWith can show it.
package main

import (
	"sync/atomic"
	"time"

	"github.com/go-widgets/webcanvas"
)

type scene struct {
	red     atomic.Bool
	request func()
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
	webcanvas.Run("screen", s)
}
