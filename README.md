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
all: it compiles for the browser and is exercised by the applications that use
it, not by a test here. A gate that reports 100% while a file is invisible to
it is worth saying out loud.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-widgets/webcanvas authors.
