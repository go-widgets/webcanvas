// Copyright (c) the go-widgets/webcanvas authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import "testing"

type plainApp struct{}

func (plainApp) Size() (int, int)      { return 1, 1 }
func (plainApp) Draw([]byte)           {}
func (plainApp) Click(int, int) bool   { return false }
func (plainApp) Move(int, int) bool    { return false }
func (plainApp) Release(int, int) bool { return false }
func (plainApp) Context(int, int) bool { return false }
func (plainApp) Char(string) bool      { return false }
func (plainApp) KeyDown(string) bool   { return false }

type modApp struct {
	plainApp
	got []Modifiers
}

func (m *modApp) Modifiers(mod Modifiers) { m.got = append(m.got, mod) }

func TestModifiersReachAModifierAwareScene(t *testing.T) {
	a := &modApp{}
	tellModifiers(a, Modifiers{Ctrl: true})
	tellModifiers(a, Modifiers{Shift: true, Meta: true})
	if len(a.got) != 2 || !a.got[0].Ctrl || !a.got[1].Shift || !a.got[1].Meta || a.got[1].Ctrl {
		t.Fatalf("got %+v", a.got)
	}
	// A scene without the method is left alone.
	tellModifiers(plainApp{}, Modifiers{Alt: true})
}
