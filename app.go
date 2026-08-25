// Package webcanvas is the browser harness of the go-widgets stack: the
// generic glue that blits an RGBA framebuffer into a <canvas> and routes DOM
// pointer and keyboard events into a widget scene. It carries no widget logic
// of its own — an application implements the small [App] interface and hands
// it to [Run], so the DOM plumbing lives in exactly one place instead of being
// written again per application.
//
// The interface is defined here, in a tag-less file, so a native (non-wasm)
// build and its tests can name the type and assert that a scene satisfies it;
// the DOM implementation of [Run] lives in run_js.go behind a `js && wasm`
// build tag, so it drops out of the native build entirely.

package webcanvas

import (
	"fmt"
	"os"
	"runtime/debug"
)

// App is a self-contained canvas scene. A host (the wasm [Run] loop, or a
// native test) owns the pixel buffer and the event source; the App owns the
// widgets. Every coordinate is in canvas-local pixels (top-left origin), the
// same space [Run] derives from the pointer event's position within the
// canvas' bounding rectangle.
//
// Each event method reports whether the scene changed and therefore needs a
// repaint, so the host can skip a redraw when nothing moved. Draw is expected
// to fully paint the buffer (it is never given a dirty region).
type App interface {
	// Size returns the fixed pixel dimensions of the scene's surface. The host
	// sizes the canvas and allocates the framebuffer from it; it is read once,
	// at startup, so it must not change over the App's life.
	Size() (w, h int)

	// Draw paints the whole scene into buf, a width*height*4 RGBA byte slice
	// laid out exactly like an image.RGBA's Pix (row-major, 4 bytes/pixel).
	Draw(buf []byte)

	// Click delivers a primary (left) button press at (x, y). It begins a
	// gesture — a selection, a drag, a placement — that later Move/Release
	// calls advance and commit.
	Click(x, y int) bool

	// Move delivers a pointer move at (x, y). While a Click gesture is in
	// flight it is a drag tick; otherwise it is a hover.
	Move(x, y int) bool

	// Release delivers the primary button release at (x, y), committing any
	// in-flight gesture Click began.
	Release(x, y int) bool

	// Context delivers a secondary (right) button press at (x, y), typically
	// opening a context menu. The host suppresses the browser's own menu.
	Context(x, y int) bool

	// Char delivers a single printable character typed with no Ctrl/Meta/Alt
	// modifier — text input for a focused field.
	Char(s string) bool

	// KeyDown delivers a named key (Enter, Backspace, Delete, Arrow*, …) or a
	// modified key press, routed to the focused widget.
	KeyDown(s string) bool
}

// Ticker is an optional companion to [App]: a scene that needs a steady
// animation clock (a toast countdown, a blinking caret) implements it, and
// [Run] installs a 60-Hz timer that calls Tick and repaints. A scene with no
// time-varying state omits it, and Run installs no timer, so it never repaints
// except in response to input.
type Ticker interface {
	// Tick advances one animation frame. Run repaints after every Tick.
	Tick()
}

// Animator is an optional companion to [App]: a scene with time-varying content
// driven by a REAL wall clock (procedurally animated icons, say) implements it,
// and [Run] installs a requestAnimationFrame loop that hands it the elapsed dt
// between frames — in seconds — through AnimationStep. Unlike [Ticker] (a fixed
// cadence that always repaints), an Animator advances by the true frame delta and
// reports whether the frame changed anything, so Run repaints only when a pixel
// actually moved. A scene that implements neither installs no clock and repaints
// on input alone. The phase-advance logic lives in the scene (natively testable);
// only the rAF wiring is browser-side.
type Animator interface {
	// AnimationStep advances the scene's animation by dt seconds of real elapsed
	// time and reports whether the scene now needs a repaint.
	AnimationStep(dt float64) (repaint bool)
}

// Resizer is an optional companion to [App]: a scene that can adapt its layout to
// a NEW surface size implements it, and [Run] installs a window "resize" listener
// (and fits the canvas to the viewport once at startup) that re-sizes the canvas
// and framebuffer, calls Resize, and repaints. A scene that omits it keeps the
// fixed [App.Size] forever — the pre-resize behaviour every existing demo relies
// on — so Run never installs the listener and the surface never changes.
//
// Resize is handed the target pixel size (the canvas' laid-out client box) and
// returns the size it will actually render at: a scene may clamp to a sane
// minimum, and the host allocates the framebuffer from the RETURNED size, so the
// scene and the buffer can never disagree. The relayout logic lives in the scene
// (natively testable); only the DOM resize wiring is browser-side.
type Resizer interface {
	// Resize relays out the scene to fit w×h device pixels and returns the pixel
	// size (rw, rh) it will render at — the size the host sizes the canvas and
	// framebuffer to.
	Resize(w, h int) (rw, rh int)
}

// Scroller is an optional companion to [App]: a scene with a scrollable region
// (a docked list, an icon palette, an overflowing panel) implements it, and [Run]
// installs a "wheel" listener that translates the browser's WheelEvent into
// toolkit scroll ROWS and routes them — with the canvas-local pointer position, so
// the scene can hit-test which region the wheel is over — through Scroll. A scene
// that omits it installs no wheel listener, so the page keeps its default wheel
// behaviour and every existing demo (the widget gallery) is unchanged.
//
// dx / dy are the horizontal / vertical scroll amounts in toolkit ROWS (already
// normalised from the event's deltaMode by [scrollRows]): positive dy scrolls
// down / forward, positive dx scrolls right. A scene typically forwards them to
// the widget under (x, y) as a [toolkit.Event] of kind EventScroll (Delta = dy,
// DeltaX = dx), which scrollable widgets clamp at both ends. Scroll reports
// whether the scene changed and therefore needs a repaint.
type Scroller interface {
	// Scroll delivers a wheel / trackpad scroll of dy vertical and dx horizontal
	// ROWS at canvas-local (x, y), and reports whether the scene needs a repaint.
	Scroll(x, y, dx, dy int) (repaint bool)
}

// A browser WheelEvent reports its delta in one of three units, named by its
// deltaMode: pixels (0, the default a mouse notch and a trackpad use), lines (1,
// some Firefox setups) or pages (2). [scrollRows] normalises each to toolkit rows.
const (
	deltaModeLine = 1
	deltaModePage = 2

	// wheelLinePixels is the nominal CSS px one scroll ROW spans when a wheel event
	// reports its delta in pixels — a typical text-line height. A one-notch wheel
	// (~100 px in Chrome) then moves a couple of rows; a slow trackpad glide fewer.
	wheelLinePixels = 40
	// wheelPageRows is how many rows one page-mode (deltaMode 2) unit scrolls.
	wheelPageRows = 10
)

// scrollRows converts one axis of a browser WheelEvent — delta, in the unit named
// by mode — into toolkit scroll ROWS, the unit [toolkit.Event.Delta] carries. A
// pixel delta (mode 0, the default) is divided by a nominal line height; a line
// delta (mode 1) passes straight through; a page delta (mode 2) is multiplied out.
// The sign is preserved and any nonzero delta yields at least one row in its
// direction, so a small trackpad nudge still scrolls rather than rounding away to
// nothing; a zero delta is zero rows.
func scrollRows(delta float64, mode int) int {
	if delta == 0 {
		return 0
	}
	var rows float64
	switch mode {
	case deltaModeLine:
		rows = delta
	case deltaModePage:
		rows = delta * wheelPageRows
	default: // pixel mode (0) and any unknown mode
		rows = delta / wheelLinePixels
	}
	if n := int(rows); n != 0 { // int() truncates toward zero
		return n
	}
	if delta > 0 {
		return 1
	}
	return -1
}

// PanicReporter logs a handler panic the [guard] net recovered. The wasm host
// swaps in a console.error reporter carrying the JS stack; the default writes to
// standard error so a native run — and the recover test — still surfaces it. It
// is a package var, not a const, precisely so run_js.go can replace it.
var PanicReporter = func(r any, stack []byte) {
	fmt.Fprintf(os.Stderr, "webcanvas: recovered panic in a handler: %v\n%s\n", r, stack)
}

// guard runs fn under a recover net: a panic escaping a single event handler (or
// the paint it triggers) is caught, reported through [PanicReporter] with its
// stack, and swallowed — so ONE bad frame logs an error instead of tearing the
// whole wasm instance off the page, and the next dispatch still runs. It reports
// whether fn panicked so a test can prove the net fired. It is deliberately a
// last-resort net IN ADDITION to the toolkit's own per-widget hardening, never a
// substitute for fixing the upstream bug.
func guard(fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			PanicReporter(r, debug.Stack())
		}
	}()
	fn()
	return false
}
