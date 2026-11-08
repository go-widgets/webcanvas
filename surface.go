// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

// This file is the pure half of a [Surface]: the routing decisions and the
// listener ledger, with no syscall/js, so they are tested natively at 100%.
// surface_js.go is the DOM glue that calls them.

// ScrollConsumer is [Scroller] for a page that holds more than one canvas, or a
// canvas among prose: the scene says whether it USED the wheel. When consumed is
// false the host does not call preventDefault, so the page scrolls under the
// pointer as if the canvas were not there. A canvas editor that has no vertical
// scroll left to give, or whose pointer is over a part of the scene that does
// not scroll, returns (false, false).
//
// Why "not calling preventDefault" is the whole mechanism (WHATWG DOM,
// https://dom.spec.whatwg.org/ §2.9 and §2.7): Event.preventDefault() sets the
// canceled flag only if the event's "in passive listener flag" is unset, and
// that flag is set while a listener whose "passive" is true runs. The "default
// passive value" is true for wheel, mousewheel, touchstart and touchmove only
// when the target is a Window, a Document, its document element or its body;
// for a <canvas> the default is false. A surface therefore registers its wheel
// listener with {passive: false}, so that preventDefault CAN cancel the scroll
// when the scene consumed the event, and simply does not call it when not.
//
// A scene implements Scroller or ScrollConsumer, not both: the method name is
// shared. Scroller keeps its old meaning, the wheel is always consumed.
type ScrollConsumer interface {
	// Scroll delivers a wheel scroll of dy vertical and dx horizontal ROWS at
	// canvas-local (x, y). repaint: the scene changed. consumed: the scene used
	// the event, so the page must not scroll.
	Scroll(x, y, dx, dy int) (repaint, consumed bool)
}

// wheelRoute is what a surface does with one wheel event.
type wheelRoute struct {
	// Listens: the scene takes the wheel at all. False means no listener is
	// installed and the page keeps its default behaviour.
	Listens bool
	// Repaint: draw a frame.
	Repaint bool
	// PreventDefault: stop the page from scrolling.
	PreventDefault bool
}

// hasWheel reports whether app takes the wheel (Scroller or ScrollConsumer).
func hasWheel(app App) bool {
	switch app.(type) {
	case ScrollConsumer, Scroller:
		return true
	}
	return false
}

// routeWheel delivers a wheel event to app and says what the host must do. A
// [Scroller] always consumes the wheel (its historical behaviour, so the single
// canvas API is unchanged); a [ScrollConsumer] decides per event.
func routeWheel(app App, x, y, dx, dy int) wheelRoute {
	switch sc := app.(type) {
	case ScrollConsumer:
		repaint, consumed := sc.Scroll(x, y, dx, dy)
		return wheelRoute{Listens: true, Repaint: repaint, PreventDefault: consumed}
	case Scroller:
		return wheelRoute{Listens: true, Repaint: sc.Scroll(x, y, dx, dy), PreventDefault: true}
	}
	return wheelRoute{}
}

// isTextKey reports whether a keydown is text for [App.Char]: one rune, with no
// Ctrl, Meta or Alt held. Everything else is a [App.KeyDown].
func isTextKey(key string, ctrl, meta, alt bool) bool {
	n := 0
	for range key {
		n++
		if n > 1 {
			return false
		}
	}
	return n == 1 && !ctrl && !meta && !alt
}

// listeners is the ledger of everything a surface attached to the page: DOM
// listeners, timers, animation frames. Close runs every detach, newest first, so
// nothing the surface installed outlives it. Not safe for concurrent use; the
// DOM is single-threaded.
type listeners struct {
	detach []func()
	closed bool
}

// add runs attach now and records detach for [listeners.close]. After close, add
// does nothing: a late registration would leak.
func (l *listeners) add(attach, detach func()) {
	if l.closed {
		return
	}
	attach()
	l.detach = append(l.detach, detach)
}

// count is the number of live registrations.
func (l *listeners) count() int { return len(l.detach) }

// close detaches everything, newest first. It is idempotent.
func (l *listeners) close() {
	l.closed = true
	for i := len(l.detach) - 1; i >= 0; i-- {
		l.detach[i]()
	}
	l.detach = nil
}

// clear detaches everything but stays open, for a group that is rebound while
// the surface lives (the keyboard target moving to a mirror root).
func (l *listeners) clear() {
	for i := len(l.detach) - 1; i >= 0; i-- {
		l.detach[i]()
	}
	l.detach = nil
}
