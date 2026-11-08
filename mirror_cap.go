// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"slices"
	"strconv"
	"strings"
)

// isActivationKey reports whether a keydown is one the browser turns into a
// click on a focused button. UI Events (https://www.w3.org/TR/uievents/#event-type-keydown):
// the default action of keydown "MUST be to dispatch a click event" when the key
// is Enter or Space and focus is on a state-changing element, and "if this
// event is canceled, the associated event types MUST NOT be dispatched". The
// HTML button's activation behavior (HTML Standard, the button element) never
// looks at the canceled flag of the click, so the only place to stop the
// browser's own click is the keydown. The mirror therefore cancels the keydown
// itself for these keys when the target is a mirror element, hands the key to
// the scene, and replays ONE click only if the scene did not consume it: one key
// press, one activation. (Some browsers also click on the keyup of Space, so
// that keyup is canceled too.)
func isActivationKey(key string) bool { return key == "Enter" || key == " " }

// keyClickWindowMS is how long after a keyboard activation a native click with
// no pointer (detail 0) is taken as that same activation's echo.
const keyClickWindowMS = 500

// isKeyboardEcho reports whether a click event is the browser's own click for a
// key press the mirror already activated: it has no pointer (UIEvent detail 0,
// which is also what an assistive technology's click has) and follows the
// keyboard activation at keyAt within [keyClickWindowMS]. Cancelling the keydown
// is the spec'd way to stop it (see [isActivationKey]), but a browser that
// delivers the click anyway (measured: Chrome driven through CDP's
// Input.dispatchKeyEvent for Enter) must still not activate twice, so the click
// listener drops it. keyAt <= 0 means no activation yet.
func isKeyboardEcho(detail int, keyAt, now float64) bool {
	return detail == 0 && keyAt > 0 && now-keyAt < keyClickWindowMS
}

// maxMirrorNodes bounds the DOM one mirror builds, whatever the scene returns.
// A tree above it is cut by [capNodes]. At the measured 72 ms for 2,000 nodes,
// 5,000 build in about 0.2 s and keep the DOM a screen reader must walk to a
// size it can; larger documents are W4's paged mirror (#21).
const maxMirrorNodes = 5000

// truncatedKey is the Key of the synthetic status node [capNodes] adds. A real
// key could in principle be the same string; the synthetic node comes first, so
// it wins the first-key-kept rule and the real one is the one dropped.
const truncatedKey = "\x00webcanvas-truncated"

// capNodes returns ns unchanged when it has at most max nodes. Above that it
// returns exactly max nodes, in the original order:
//
//   - a status node first, saying the view is truncated and by how much (it is
//     announced like any new status);
//   - the focused node and its subtree (keys under focus+"/"), up to half the
//     budget, so what the user is operating is always in the mirror;
//   - then the first nodes in reading order until the budget is spent.
//
// truncated reports whether anything was cut. max must be at least 2.
func capNodes(ns []A11yNode, max int) (out []A11yNode, truncated bool) {
	if len(ns) <= max {
		return ns, false
	}
	budget := max - 1 // one slot is the status
	keep := make([]bool, len(ns))
	used := 0
	if f := slices.IndexFunc(ns, func(n A11yNode) bool { return n.Focused }); f >= 0 {
		keep[f] = true
		used++
		prefix := ns[f].Key + "/"
		for i, n := range ns {
			if used >= budget/2+1 {
				break
			}
			if !keep[i] && strings.HasPrefix(n.Key, prefix) {
				keep[i] = true
				used++
			}
		}
	}
	for i := range ns {
		if used >= budget {
			break
		}
		if !keep[i] {
			keep[i] = true
			used++
		}
	}
	out = make([]A11yNode, 0, max)
	out = append(out, A11yNode{Key: truncatedKey, Role: "status",
		Name: "Accessibility view truncated: showing " + strconv.Itoa(used) + " of " + strconv.Itoa(len(ns)) + " items"})
	for i, n := range ns {
		if keep[i] {
			out = append(out, n)
		}
	}
	return out, true
}
