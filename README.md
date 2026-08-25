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
to be told about time, animation, a resized canvas or the wheel.

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
