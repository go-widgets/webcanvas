// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"strings"
	"testing"
)

type codeApp struct {
	plainApp
	codes []string
}

func (c *codeApp) KeyCode(code string) { c.codes = append(c.codes, code) }
func (c *codeApp) KeyUp(string) bool   { return true }

var (
	_ KeyCodeAware = (*codeApp)(nil)
	_ KeyUpper     = (*codeApp)(nil)
)

func TestTellKeyCode(t *testing.T) {
	tellKeyCode(plainApp{}, "KeyA") // a scene without the interface: nothing
	c := &codeApp{}
	tellKeyCode(c, "KeyA")
	tellKeyCode(c, "")
	tellKeyCode(c, strings.Repeat("X", maxCode+1))
	if strings.Join(c.codes, ",") != "KeyA,," {
		t.Errorf("codes = %q", c.codes)
	}
}
