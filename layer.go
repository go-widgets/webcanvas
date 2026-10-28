// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

// Image is a browser image the host can draw over the scene: an ImageBitmap,
// a <canvas>, an OffscreenCanvas, an <img> -- anything CanvasRenderingContext2D
// drawImage accepts. The scene holds it as an opaque handle: a syscall/js
// Value in the browser; in a native build or test any value, which the host
// never draws (there is no canvas to draw it on).
type Image struct{ handle any }

// NewImage wraps handle (a js.Value in the browser) as an Image.
func NewImage(handle any) Image { return Image{handle: handle} }

// Handle is the value NewImage was given.
func (i Image) Handle() any { return i.handle }

// IsZero reports whether i holds nothing.
func (i Image) IsZero() bool { return i.handle == nil }

// Layer is one image drawn over the scene after [App.Draw], scaled into its
// rectangle. Coordinates are canvas pixels, the same space Draw paints: device
// pixels for a [PixelRatioAware] scene, so an image lands sharp at any ratio.
type Layer struct {
	Image      Image
	X, Y, W, H int
	// Alpha is the opacity in (0, 1]; 0 (the zero value) means opaque.
	Alpha float64
}

// Layered is an optional companion to [App]: a scene that shows images the
// browser already holds -- frames a GPU rendered into an ImageBitmap, a video
// -- implements it, rather than copying their pixels through Draw's buffer.
// After every Draw, [Run] calls Layers and draws each one, in order, with
// drawImage scaled into its rectangle (smoothing on, quality high). Each
// layer is drawn under its own recover net, so one unusable image (a closed
// ImageBitmap throws InvalidStateError) costs that layer only.
//
// The images are the scene's: Run never closes or keeps them past the call.
// A scene that omits the interface draws exactly as before.
type Layered interface {
	Layers() []Layer
}

// MaxLayers bounds the layers drawn per frame.
const MaxLayers = 64

// drawable normalises l for a w×h canvas: a layer with no image, an empty
// rectangle, a rectangle entirely off the canvas, or an alpha that is not a
// number in [0, 1] is not drawn.
func drawable(l Layer, w, h int) (Layer, bool) {
	if l.Image.IsZero() || l.W <= 0 || l.H <= 0 {
		return l, false
	}
	if l.X >= w || l.Y >= h || l.X+l.W <= 0 || l.Y+l.H <= 0 {
		return l, false
	}
	if !(l.Alpha >= 0 && l.Alpha <= 1) { // also catches NaN
		return l, false
	}
	if l.Alpha == 0 {
		l.Alpha = 1
	}
	return l, true
}

// layersOf is the bounded, drawable layers of app for a w×h canvas.
func layersOf(app App, w, h int) []Layer {
	la, ok := app.(Layered)
	if !ok {
		return nil
	}
	var out []Layer
	for _, l := range la.Layers() {
		if len(out) == MaxLayers {
			break
		}
		if l, ok := drawable(l, w, h); ok {
			out = append(out, l)
		}
	}
	return out
}
