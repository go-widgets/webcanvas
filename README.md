# webcanvas

[![CI](https://github.com/go-widgets/webcanvas/actions/workflows/ci.yml/badge.svg)](https://github.com/go-widgets/webcanvas/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-widgets/webcanvas.svg)](https://pkg.go.dev/github.com/go-widgets/webcanvas)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-widgets/webcanvas)](https://goreportcard.com/report/github.com/go-widgets/webcanvas)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

Runs a [go-widgets](https://github.com/go-widgets) scene in a browser tab, on a
plain `<canvas>`: no compositor, no `SharedArrayBuffer`, no cross-origin
isolation, nothing on the far end. It blits an RGBA framebuffer into the canvas
and routes DOM pointer and keyboard events back into the scene.

It carries no widget logic of its own. An application implements a small
interface and hands it to `Run`:

```go
//go:build js && wasm

package main

import "github.com/go-widgets/webcanvas"

func main() { webcanvas.Run("screen", myScene()) }
```

**Several canvases on one page.** `Run` is the single-canvas entry point and
never returns. `Mount(canvas js.Value, app App) *Surface` puts a scene on any
`<canvas>` and returns at once; `Run` is `Mount`, `Focus`, park. Every
listener belongs to its surface, so two surfaces never see each other's input:

- **Keys** are heard by the surface that has focus, and only by it. The canvas
  is made focusable (`tabindex="0"` unless the page set one), so a click or Tab
  focuses it, and `Surface.Focus()` does it from code. Before, the keyboard
  listeners were on `window`, and every canvas on the page received every key.
  `Run` focuses its canvas, so a one-canvas page still types from the start;
  click elsewhere on the page and the keys stop, as they do for any focusable
  element.
- **The wheel** goes to the surface under the pointer. A `Scroller` always
  takes it (`preventDefault`), as before. A scene that implements
  `ScrollConsumer`, `Scroll(x, y, dx, dy int) (repaint, consumed bool)`, takes
  it only when it used it: with `consumed == false` the page scrolls under the
  canvas, so an editor among prose does not trap the wheel. The listener is
  registered `{passive: false}`: per the WHATWG DOM Standard, `preventDefault`
  does nothing while a listener's passive flag is set, and wheel listeners
  default to passive only on `window`, `document`, its root element and `body`,
  not on a canvas.
- **`Surface.Close()`** removes every listener, timer and animation frame the
  surface installed, and is idempotent; the other surfaces carry on. A
  `Presenter` has no teardown of its own.

`Surface.Canvas()` is the canvas. The key target is the canvas for now; it is
kept in one place so that an accessibility mirror can take it over later
without changing this API
([#21](https://github.com/go-widgets/webcanvas/issues/21)).
`browsertest/` mounts two surfaces among prose in headless Chrome: a key typed
after a click reaches only that surface, a real wheel over a scene that does
not consume it scrolls the page, one over a scene that does leaves it, and
after `Close` every `addEventListener` is matched by a `removeEventListener`.

`App` is `Size`, `Draw(buf []byte)`, and one method per kind of event, each
reporting whether the scene changed so a repaint can be skipped when nothing
did. A scene may also implement `Ticker`, `Animator`, `Resizer` or `Scroller`
to be told about time, animation, a resized canvas or the wheel, and
`ModifierAware` to be told the state of Ctrl, Shift, Alt and Meta before every
pointer, keyboard and wheel event -- without it Ctrl+A, Cmd+A and Alt+A all
reach `KeyDown` as "a", and a Shift-click is a click.

**HiDPI.** A `Resizer` scene that also implements `PixelRatioAware` renders at
the display's device pixels: the canvas backing store becomes its CSS box ×
`window.devicePixelRatio`, the scene is told the ratio (a go-widgets scene
forwards it to `toolkit.SetMetricScale`) and gets device pixels in `Resize`,
and both follow browser zoom and moves between displays (a `matchMedia`
resolution query, as MDN's `Window.devicePixelRatio` shows). Without it a
Retina display upscales a 1× bitmap: blurred, stair-stepped text. The page
gives the canvas its CSS size (`width: 100vw; height: 100vh`).

A scene whose content changes while nobody touches it -- a fetch that
completes, a message that arrives -- implements `RepaintWith(request func())`
(`RepaintAware`). `request` asks for one frame, from any goroutine, coalesced
into the next animation frame. Without it such a scene shows its first frame
until the next click. `browsertest/` checks this in headless Chrome, and fails
when the hook is removed.

**Key positions and releases.** A scene that implements `KeyCode(code string)`
(`KeyCodeAware`) is told each keyboard event's physical key, the DOM's
`KeyboardEvent.code` ("KeyA", "ArrowUp", "ShiftLeft"), just before the event,
the way `ModifierAware` is told the modifiers. The key ("a", "A", "é") is what
was typed. Its code is where the key is, "not affected by the current
keyboard layout or modifier state" (W3C UI Events), which is what a game, or a
guest that expects key positions, needs. A code longer than 32 bytes is
passed as "". A scene that implements `KeyUp(key string) bool` (`KeyUpper`)
also gets key releases, with the same key values and the code and modifiers
first. `Run` installs the keyup listener only for such a scene. Scenes that
implement neither see exactly the events they did before. `browsertest/`
types Shift+A and checks `char A KeyA shift=true` and
`up A KeyA shift=true`.

**Images the browser already holds.** A scene can show frames a GPU rendered
into an `ImageBitmap`, a video, or another canvas without copying their pixels
through `Draw`'s buffer: it implements `Layers() []Layer` (`Layered`). Each
layer is an opaque `Image` (`NewImage(jsValue)`), a rectangle in canvas
pixels (device pixels for a `PixelRatioAware` scene) and an optional alpha.
After every `Draw`, `Run` calls `drawImage` for each layer, in order, scaled
into its rectangle with high-quality smoothing.

Bounds and safety:
- At most 64 layers are drawn per frame.
- A layer with no image, an empty rectangle, a rectangle entirely off the canvas, or an alpha that is not in [0, 1] is skipped.
- Each layer is drawn under its own recover net, so a closed `ImageBitmap`, which makes `drawImage` throw, costs only that layer.
- The images remain the scene's: `Run` never closes or keeps them.

`browsertest/` draws a closed bitmap, then a 2×2 green bitmap scaled into a
4×4 corner, and reads the pixels back.

**Windows over content the host draws as DOM.** A scene may draw some of its
content as real DOM rather than into the canvas — a page of text the browser
lays out and the reader can select, a video, an `<iframe>` — and still paint
windows of its own over it. It cannot, by default:

> **A DOM element is unconditionally above a canvas.** The canvas and the
> element are siblings in the page, so a window painted *into* the canvas
> cannot come forward over one, however the application stacks it internally.

There is no z-index that fixes this and no drawing order that avoids it. The
only way a canvas-painted window appears over DOM content is for the host to
punch a hole in the DOM content where the window is. `ClipPath([]Overlay,
dpr)` builds that hole as a CSS `clip-path`; the host applies it to the element
holding its DOM content, and `clip-path: none` when the string is empty.

- Coordinates are canvas pixels, the same space `Draw` paints and `Layer` is
  placed in; `dpr` converts them to the CSS pixels a `path()` reads.
- Each `Overlay` carries the **corner radius** of the window drawn there. A
  square hole behind a rounded window cuts the content out at the corners while
  the canvas leaves them transparent, and the void between the two reads as
  black notches around the window.
- The path is the outer ring plus **one closed subpath per hole**, filled
  `evenodd`. A single polygon threading from the ring in to each hole and back
  has that journey drawn, and leaves a wedge cut out of the content.
- At most 64 holes per frame; empty rectangles punch nothing and do not use up
  the budget; a radius past half the shorter side is clamped; a `dpr` that is
  not a positive number is taken as 1 rather than producing `NaN` coordinates,
  which a browser rejects — and a rejected clip path is no clip path.

Measured in the [go-tex playground](https://github.com/go-tex/go-tex.github.io),
which is where it comes from: dragging its find-and-replace panel over the
rendered page sliced the panel off at the page's edge until the hole existed.

## Accessibility

A `<canvas>` is one opaque image to a screen reader. A scene that implements
`A11yAware`, `A11yNodes() []A11yNode`, gets an **accessibility mirror**: a DOM
subtree beside the canvas that says in ARIA what the scene paints. `Mount` and
`Run` build it; nothing else changes for a scene that does not implement it.

```go
func (s *scene) A11yNodes() []webcanvas.A11yNode {
	return []webcanvas.A11yNode{
		{Key: "Button#0", Role: "button", Name: "Save", X: 10, Y: 20, W: 80, H: 30},
		{Key: "@cell3", Role: "textbox", Name: "Go cell 3", Value: src, Multiline: true, X: 150, Y: 20, W: 200, H: 80},
	}
}
```

- **Nodes** are in reading order, in surface pixels. `Key` is stable across
  frames (toolkit's `A11yNode.Key`); the mirror diffs by it, so an unchanged
  key keeps the same DOM element and a screen reader's virtual cursor is not
  reset on every frame.
- **Roles:** button (a native `<button>`), checkbox, switch and radio
  (`aria-checked`), slider, progressbar, meter and spinbutton
  (`aria-valuemin/max/now/valuetext`), text, textbox (`role=textbox`,
  `aria-multiline`, `aria-readonly`), status, alert and log, and any other
  WAI-ARIA role name. `Disabled` is `aria-disabled`.
- **Hidden visually, not from assistive technology:** the root is clipped
  (`clip-path: inset(50%)`, `overflow: hidden`), never `display: none`,
  `visibility: hidden` or `aria-hidden`, and has `pointer-events: none`, so
  the mouse still reaches the canvas.
- **Focus:** the mirror element of the focused node holds real DOM focus, and
  the mirror root is the surface's key target.
- **Activation:** a `click` on a mirror element (a screen reader's "activate")
  is replayed as a press and release at the node's centre; so is a focus that
  assistive technology moves onto a focusable node.
- **Announcements:** `status` and `log` text that appears or changes is written
  to one persistent polite live region, `alert` text to an assertive one. Both
  exist, empty, before the first announcement.
- **Cost:** a changed tree is published at most every 200 ms, and a change
  refused as too soon is deferred, never dropped. An unchanged tree costs a
  64-bit signature. A resize and a change of the focused node are never held
  back. `Surface.Refresh()` draws a frame and updates the mirror for a host that
  changed the scene from outside.
- **Key activation:** Enter or Space on a focused mirror element activates
  exactly once. The mirror cancels the keydown (UI Events: a canceled keydown
  dispatches no click), hands the key to the scene, and replays one click only
  if the scene did not consume it. A native click that still follows a handled
  key (detail 0, within 500 ms) is dropped.
- **Node cap:** a mirror never holds more than **5,000** nodes, whatever the
  scene returns. Above that, the tree is cut to the 5,000 that matter: a status
  node first ("Accessibility view truncated: showing 4999 of N items", announced
  like any status), the focused node and its subtree (at most half the budget),
  then the first nodes in reading order. Larger documents need the paged mirror
  planned in #21 (W4).
- **Untrusted text:** names, values and roles may come from the user (a notebook
  from anyone). They reach the DOM only through `textContent` and
  `setAttribute`; there is no `innerHTML`. A role that is not lowercase letters
  and hyphens is dropped.

Measured in headless Chrome, 2,000 nodes (`browsertest`, `go test -v -run
TestMirrorCost`): building the tree 70 ms, one changed node 6.5 ms, an unchanged
frame 0.5 ms, every node moved 36 ms, every node reordered 119 ms.

## The text layer

A scene that also implements `TextInputHost` (`TextInputFor(key string)
textinput.Client`, from [go-widgets/textinput](https://github.com/go-widgets/textinput))
gets a **text layer** for each textbox node it publishes
([#21](https://github.com/go-widgets/webcanvas/issues/21), W3). The layer
replaces the plain `div role=textbox`: it is the widget's only accessibility
element, its editing host and the text the browser's find and selection see.

- **Structure:** one `div` per model line, placed at the model's line position
  (`InputRect`), the runes of each line put on the toolkit's cell grid by
  `letter-spacing` from a measured width table (`planLine`). Text and caret are
  transparent, so the canvas still paints the glyphs. `white-space: pre`,
  `tab-size: 1`. The browser paints the selection and the find matches
  (`::selection`, `::search-text`).
- **Editing:** `contenteditable="plaintext-only"` only while the layer has
  focus, so a selection dragged between prose and code is an ordinary one.
  `keydown` goes through `routeKey`: navigation and modified keys to
  `App.KeyDown`, printable keys not at all; text arrives through `beforeinput`
  (`routeInput`) and is applied once with `ReplaceInput`. A composition runs in
  place in the DOM line and reaches the model through `SetMarkedInput`
  (`compositionEdit`); `compositionend` commits and rewrites the line.
  `selectionchange` calls `SetInputSelection`. An `input` the layer did not
  understand goes through `textinput.Reconcile`.
- **Bounds:** 32 layers per surface, 20,000 lines per layer, 4,000 UTF-16 units
  per line. Text reaches the DOM through `textContent` only.
- **Tests:** the pure part is at 100% coverage. `browsertest/` checks, in
  headless Chrome, a positioning error under 0.05 px at device pixel ratios 1
  and 2, one textbox in the AX tree whose value is the code and no second copy,
  `window.find`, a selection from prose into code and from code into prose, one
  "a" for one keystroke, `Input.imeSetComposition` and its commit, and the
  bound. `TEXTLAYER_COST=1 go test -run TestTextLayerKeystrokeCost -v` prints
  the keystroke cost table.
- **Not checked:** that a Ctrl+F match is visible over the canvas in Chrome,
  Safari and Firefox (it needs a headed browser), and the canvas still paints
  its own selection band.

## The clipboard

A canvas has no clipboard of its own. A widget toolkit that keeps one in
memory (go-widgets/toolkit's default) copies and pastes only inside the page.
`NewClipboard()` connects it to the browser's clipboard. Install it once at
startup:

```go
toolkit.SetClipboard(webcanvas.NewClipboard())
```

- **Copy:** the text also goes to `navigator.clipboard.writeText`. The copy
  runs inside the keydown that asked for it, which is the user activation the
  Clipboard API requires. A refused write keeps the copy inside the page.
- **Paste:** the only synchronous read that needs no permission prompt is a
  paste event's `clipboardData`. So Ctrl+V and Cmd+V (Shift allowed, Alt not)
  are held at keydown and not prevented. The browser fires paste on the
  focused canvas, the bridge takes its text, and only then is the key
  delivered: the widget's own paste reads what the user copied elsewhere. If
  no paste event comes, the key is delivered anyway, as before the bridge.
  The key a scene sees does not change, only when it arrives: a terminal that
  sends Ctrl+V as `^V` still does.
- **The bound:** a paste keeps at most 1 MiB, cut on a rune boundary.
- **The Edit menu:** a paste from the browser's Edit menu refreshes the text
  without pressing a key.

Checked in headless Chrome (`browsertest/clipboard_test.go`): the key
arrives once, after the paste event, carrying its text; with no paste event
it arrives once, from the fallback; a copy calls `writeText`. The paste event
in that test is dispatched by the page, because headless Chrome offers no way
to choose what its clipboard holds. **Not checked by a test:** a real Cmd+V
in a headed Chrome, Firefox or Safari, in particular whether Safari fires
paste on a focused, non-editable canvas. Where it does not, the fallback keeps
the old behaviour.

## On the GPU

`RunWith(id, app, presenter)` hands the canvas to a `Presenter` instead of
painting `Draw`'s buffer with `putImageData`. Package `webcanvas/gpu` is a
presenter that draws on the GPU through WebGPU:

```go
webcanvas.RunWith("screen", app, gpu.New())
```

- **Which apps:** one that also implements `gpu.Painted`, `Paint(painter.Painter)`, which paints the whole frame as it would into a `PixelPainter`.
- **How:** the app is recorded through [go-widgets/gpupainter](https://github.com/go-widgets/gpupainter) into a [go-gfx/gpu2d](https://github.com/go-gfx/gpu2d) list. gpu2d's WebGPU renderer draws it and copies the frame into the canvas texture on the GPU, so nothing passes through Go memory or `putImageData`.
- **Same pixels:** `TestThePresenterDrawsWhatDrawPaints` holds the GPU frame to `Draw`'s CPU frame byte for byte, before and after a resize. gpu2d's own test holds the canvas texture to that frame.
- **Choosing once:** a canvas takes one kind of context for life, so the choice is made once, before the first frame. The presenter asks the canvas for its WebGPU context only after it has a device, a renderer and a surface. Any earlier failure leaves the canvas untouched, and `Run` paints through 2D as before; the presenter's `Err` says why. That covers a browser without WebGPU and an app that is not `Painted`.
- **Layered scenes:** their images are drawn through the 2D context, so they are never offered to a presenter.
- **Resizing:** the renderer is rebuilt at the new size, and the old one's memory is released.

## Where this came from

It was `internal/webcanvas` inside
[go-widgets/gallery](https://github.com/go-widgets/gallery), where nothing
outside that one module could import it. It is the same code, in a place a
second application can reach — which is the point: the DOM plumbing exists
once.

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, a cross-compile across
the fleet's targets, and a `js/wasm` build.

**What that coverage does and does not say.** The interface, the scene contract
and the event dispatch are covered. `run_js.go` — the DOM loop itself — is
behind a `js && wasm` build tag and so is not part of the measured build at
all: it compiles for the browser and is exercised by `browsertest/`, a real
headless Chrome driven by chromedp (also `mirror_js.go`, the mirror's DOM glue;
the mirror's decisions are in `mirror.go` and are covered). A gate that reports 100% while a file is invisible to
it is worth saying out loud.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-widgets/webcanvas authors.
