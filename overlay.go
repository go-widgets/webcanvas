// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// This file is for scenes that draw some of their content as DOM rather than
// into the canvas -- a page of text the browser lays out and the reader can
// select, a video, an <iframe> -- and still paint windows of their own over it.
//
// ⛔⛔ A DOM ELEMENT IS UNCONDITIONALLY ABOVE A CANVAS. The canvas and the
// element are siblings in the page, so a window painted INTO the canvas cannot
// come forward over one, however the application stacks it internally. There is
// no z-index that fixes this and no drawing order that avoids it: the only way
// a canvas-painted window can appear over DOM content is for the host to punch
// a hole in the DOM content where the window is.
//
// Measured in the go-tex playground, which is where this comes from: dragging
// its find-and-replace panel over the rendered page sliced the panel off at the
// page's edge until the hole existed.

// Overlay is one rectangle the CANVAS paints on top of content the host draws
// as DOM, carrying the corner radius of the window drawn there.
//
// Coordinates are canvas pixels, the same space [App.Draw] paints and [Layer]
// is placed in: device pixels for a [PixelRatioAware] scene. Radius is in the
// same pixels; 0 is a square-cornered window.
type Overlay struct {
	X, Y, W, H int
	// Radius is the corner radius of the window drawn here. ⛔ It matters: a
	// SQUARE hole behind a ROUNDED window cuts the DOM content out at the
	// corners while the canvas leaves them transparent, and the void between
	// the two reads as black notches around the window.
	Radius int
}

// MaxOverlays bounds the holes punched per frame, as [MaxLayers] bounds the
// images drawn per frame. A clip path grows with every hole and is rebuilt
// whenever a window moves.
const MaxOverlays = 64

// clipOuter is the outer ring every clip path starts from: a box large enough
// to contain any container it is applied to, so the path's first subpath is
// "everything" and each later subpath takes a hole out of it.
//
// A number rather than the container's own size because a clip path is applied
// to an element whose box the scene does not need to know: anything at least as
// large as the element clips to the element itself.
const clipOuter = 100000

// ClipPath is the CSS clip-path that punches every overlay out of the element
// holding the host's DOM content: the outer ring, then one subpath per hole,
// filled evenodd. dpr converts the device-pixel geometry to the CSS-pixel user
// space a path() reads.
//
// It returns "" when there is nothing to punch, which the caller applies as
// clip-path: none rather than as an empty path -- an empty path clips
// everything away.
//
// ⛔ TWO SUBPATHS PER HOLE, NOT ONE POLYGON. A single path threading from the
// outer ring in to each hole and back out again has that journey DRAWN, and it
// leaves a wedge cut out of the content. The outer box plus one closed subpath
// per hole, filled evenodd, is what cuts holes and nothing else.
func ClipPath(overlays []Overlay, dpr float64) string {
	ok := punchable(overlays, dpr)
	if len(ok) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `path(evenodd, "M0 0H%dV%dH0Z`, clipOuter, clipOuter)
	for _, o := range ok {
		writeHole(&b, o, dpr)
	}
	b.WriteString(`")`)
	return b.String()
}

// punchable is the bounded, non-empty overlays of the list given, with each
// radius clamped to what its rectangle can carry. An overlay with no area
// punches no hole, and a radius past half the shorter side is not a shape.
//
// ⛔ A dpr that is not a positive number is taken as 1 rather than dropped: the
// holes are still in the right place in device pixels, and a page drawn at the
// wrong scale is a visible fault the viewer can report, where a window sliced
// off at the content's edge looks like a drawing bug in the window itself.
func punchable(overlays []Overlay, dpr float64) []Overlay {
	var out []Overlay
	for _, o := range overlays {
		if len(out) == MaxOverlays {
			break
		}
		if o.W <= 0 || o.H <= 0 {
			continue
		}
		if half := o.W / 2; o.Radius > half {
			o.Radius = half
		}
		if half := o.H / 2; o.Radius > half {
			o.Radius = half
		}
		if o.Radius < 0 {
			o.Radius = 0
		}
		out = append(out, o)
	}
	return out
}

// writeHole appends one hole's closed subpath: a rectangle, or a rounded
// rectangle when Radius > 0.
func writeHole(b *strings.Builder, o Overlay, dpr float64) {
	x0, y0 := cssPx(o.X, dpr), cssPx(o.Y, dpr)
	x1, y1 := cssPx(o.X+o.W, dpr), cssPx(o.Y+o.H, dpr)
	if o.Radius <= 0 {
		fmt.Fprintf(b, " M%s %sH%sV%sH%sZ", x0, y0, x1, y1, x0)
		return
	}
	r := cssPx(o.Radius, dpr)
	in0, in1 := cssPx(o.X+o.Radius, dpr), cssPx(o.X+o.W-o.Radius, dpr)
	up, down := cssPx(o.Y+o.Radius, dpr), cssPx(o.Y+o.H-o.Radius, dpr)
	fmt.Fprintf(b, " M%s %sH%sA%s %s 0 0 1 %s %sV%sA%s %s 0 0 1 %s %sH%sA%s %s 0 0 1 %s %sV%sA%s %s 0 0 1 %s %sZ",
		in0, y0, // start after the top-left arc
		in1,          // top edge
		r, r, x1, up, // top-right arc
		down,          // right edge
		r, r, in1, y1, // bottom-right arc
		in0,            // bottom edge
		r, r, x0, down, // bottom-left arc
		up,            // left edge
		r, r, in0, y0, // top-left arc
	)
}

// cssPx renders a device length as the plain CSS-pixel number a path() takes:
// no unit, because a path's coordinates are in the element's own user space.
func cssPx(v int, dpr float64) string {
	if !(dpr > 0) || math.IsInf(dpr, 0) { // also catches NaN
		dpr = 1
	}
	return strconv.FormatFloat(float64(v)/dpr, 'f', 1, 64)
}
