// Copyright (c) 2026 the go-widgets/gallery authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file at the root of this repository.

package webcanvas

import (
	"io"
	"os"
	"strings"
	"testing"
)

// flakyApp is a minimal [App] whose Click panics on demand while Move keeps a
// live counter, so a test can prove the recover net catches the panic AND that a
// later dispatch still runs against the same instance.
type flakyApp struct {
	moves        int
	panicOnClick bool
}

func (a *flakyApp) Size() (int, int) { return 4, 4 }
func (a *flakyApp) Draw([]byte)      {}
func (a *flakyApp) Click(int, int) bool { //nolint:revive // panics by design
	if a.panicOnClick {
		panic("boom in Click")
	}
	return true
}
func (a *flakyApp) Move(int, int) bool    { a.moves++; return true }
func (a *flakyApp) Release(int, int) bool { return false }
func (a *flakyApp) Context(int, int) bool { return false }
func (a *flakyApp) Char(string) bool      { return false }
func (a *flakyApp) KeyDown(string) bool   { return false }

// flakyApp is a genuine App (the recover net wraps exactly this dispatch surface).
var _ App = (*flakyApp)(nil)

// captureStderr redirects os.Stderr for the duration of fn and returns everything
// written to it — how the test reads the default reporter's output.
func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestGuardRecoversHandlerPanicAndKeepsDispatching is the recover-net proof: a
// handler that panics is caught by the net (not propagated to the caller — the
// wasm Run loop) and reported with its stack, and the VERY NEXT dispatch still
// executes against the same live App. This is the native stand-in for the browser
// callbacks, each of which Run wraps in exactly this guard.
func TestGuardRecoversHandlerPanicAndKeepsDispatching(t *testing.T) {
	var got any
	var sawStack bool
	orig := PanicReporter
	PanicReporter = func(r any, stack []byte) { got = r; sawStack = len(stack) > 0 }
	defer func() { PanicReporter = orig }()

	app := &flakyApp{panicOnClick: true}

	// A panicking Click is caught by the net, not propagated.
	if !guard(func() { app.Click(1, 1) }) {
		t.Fatal("guard did not report the handler panic")
	}
	if got != "boom in Click" {
		t.Fatalf("reporter saw %v, want %q", got, "boom in Click")
	}
	if !sawStack {
		t.Fatal("reporter received an empty stack")
	}

	// The next dispatch still runs — the instance survived the panic.
	if guard(func() { app.Move(2, 2) }) {
		t.Fatal("a clean handler must not report a panic")
	}
	if app.moves != 1 {
		t.Fatalf("the post-panic dispatch did not run (moves = %d, want 1)", app.moves)
	}
}

// TestGuardCleanHandlerReportsNoPanic pins the non-panicking path: guard returns
// false and runs fn to completion.
func TestGuardCleanHandlerReportsNoPanic(t *testing.T) {
	ran := false
	if guard(func() { ran = true }) {
		t.Fatal("guard reported a panic for a clean handler")
	}
	if !ran {
		t.Fatal("guard did not run the handler")
	}
}

// TestDefaultPanicReporterWritesMessageAndStack proves the built-in reporter (the
// one a native run uses) writes the recovered value and a stack to standard error.
func TestDefaultPanicReporterWritesMessageAndStack(t *testing.T) {
	out := captureStderr(func() {
		if !guard(func() { panic("kaboom") }) {
			t.Error("expected the panic to be recovered")
		}
	})
	if !strings.Contains(out, "kaboom") {
		t.Errorf("reporter output missing the panic value: %q", out)
	}
	if !strings.Contains(out, "recovered panic") {
		t.Errorf("reporter output missing its prefix: %q", out)
	}
	if !strings.Contains(out, "webcanvas") {
		t.Errorf("reporter output missing a stack frame: %q", out)
	}
}
