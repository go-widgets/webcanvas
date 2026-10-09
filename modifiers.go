// Copyright (c) the go-widgets/webcanvas authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

// Modifiers is the state of the modifier keys when an event happened, as the
// DOM event reports it (ctrlKey, shiftKey, altKey, metaKey).
type Modifiers struct {
	Ctrl, Shift, Alt, Meta bool
}

// ModifierAware is an optional companion to [App]: a scene that implements it
// is told the modifier state immediately before every pointer, keyboard and
// wheel event is delivered. Without it a modified key reaches KeyDown as its
// bare name -- "a" for Ctrl+A, Cmd+A and Alt+A alike -- and a Shift-click is a
// click: a scene could not tell copy from select-all, nor extend a selection.
// A scene that does not implement it sees exactly the events it always did.
type ModifierAware interface {
	Modifiers(m Modifiers)
}

// tellModifiers hands m to app when it wants it.
func tellModifiers(app App, m Modifiers) {
	if ma, ok := app.(ModifierAware); ok {
		ma.Modifiers(m)
	}
}
