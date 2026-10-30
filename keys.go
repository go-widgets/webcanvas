// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

// KeyCodeAware is an optional companion to [App]: a scene that implements it
// is told the physical key of every keyboard event -- the DOM's
// KeyboardEvent.code ("KeyA", "ArrowUp", "ShiftLeft") -- immediately before
// the event is delivered (Char, KeyDown, or KeyUp for a [KeyUpper]). The
// event's key ("a", "A", "é") is what was typed; its code is where on the
// keyboard it was, "not affected by the current keyboard layout or modifier
// state" (W3C UI Events, KeyboardEvent.code) -- what a game or a remote
// guest that expects key positions needs. A scene that does not implement
// it sees exactly the events it always did.
type KeyCodeAware interface {
	KeyCode(code string)
}

// KeyUpper is an optional companion to [App]: a scene that implements it is
// told when a key is released (the DOM's keyup, which a user agent "MUST
// dispatch ... when a key is released"), with the same key value KeyDown
// and Char get. [Run] installs the keyup listener only for such a scene.
// KeyUp reports whether the scene changed.
type KeyUpper interface {
	KeyUp(key string) bool
}

// maxCode bounds the code handed to a scene: every code UI Events defines
// is a short ASCII name; anything longer is not one.
const maxCode = 32

// tellKeyCode hands code to app when it wants it ("" for an unknown or
// oversized code).
func tellKeyCode(app App, code string) {
	kc, ok := app.(KeyCodeAware)
	if !ok {
		return
	}
	if len(code) > maxCode {
		code = ""
	}
	kc.KeyCode(code)
}
