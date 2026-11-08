// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"slices"
	"testing"
)

type consumerApp struct {
	flakyApp
	consume, repaint bool
	calls            int
}

func (a *consumerApp) Scroll(x, y, dx, dy int) (bool, bool) {
	a.calls++
	return a.repaint, a.consume
}

var _ ScrollConsumer = (*consumerApp)(nil)

func TestRouteWheel(t *testing.T) {
	// A plain scene takes no wheel: the page keeps it.
	if hasWheel(&flakyApp{}) || routeWheel(&flakyApp{}, 1, 2, 3, 4) != (wheelRoute{}) {
		t.Fatal("a scene without Scroller must not listen")
	}
	// A Scroller always consumes, repainting as it says.
	sc := &scrollApp{}
	if !hasWheel(sc) {
		t.Fatal("Scroller must listen")
	}
	if got := routeWheel(sc, 5, 6, 1, 2); got != (wheelRoute{Listens: true, Repaint: true, PreventDefault: true}) || sc.lastX != 5 || sc.lastDY != 2 {
		t.Fatalf("Scroller route %+v", got)
	}
	if got := routeWheel(sc, 0, 0, 0, 0); got != (wheelRoute{Listens: true, PreventDefault: true}) {
		t.Fatalf("a Scroller that did not repaint still consumes: %+v", got)
	}
	// A ScrollConsumer decides per event.
	for _, c := range []struct{ repaint, consume bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		a := &consumerApp{repaint: c.repaint, consume: c.consume}
		if !hasWheel(a) {
			t.Fatal("ScrollConsumer must listen")
		}
		want := wheelRoute{Listens: true, Repaint: c.repaint, PreventDefault: c.consume}
		if got := routeWheel(a, 1, 1, 0, 1); got != want || a.calls != 1 {
			t.Fatalf("consumer %+v: got %+v want %+v", c, got, want)
		}
	}
}

func TestIsTextKey(t *testing.T) {
	for _, c := range []struct {
		key             string
		ctrl, meta, alt bool
		want            bool
	}{
		{"a", false, false, false, true},
		{"é", false, false, false, true},
		{"世", false, false, false, true},
		{"🙂", false, false, false, true},
		{"a", true, false, false, false},
		{"a", false, true, false, false},
		{"a", false, false, true, false},
		{"Enter", false, false, false, false},
		{"", false, false, false, false},
		{"ab", false, false, false, false},
	} {
		if got := isTextKey(c.key, c.ctrl, c.meta, c.alt); got != c.want {
			t.Errorf("isTextKey(%q,%v,%v,%v) = %v", c.key, c.ctrl, c.meta, c.alt, got)
		}
	}
}

func TestListenersCloseDetachesEverythingNewestFirst(t *testing.T) {
	var l listeners
	var log []string
	reg := func(name string) {
		l.add(func() { log = append(log, "+"+name) }, func() { log = append(log, "-"+name) })
	}
	reg("a")
	reg("b")
	reg("c")
	if l.count() != 3 {
		t.Fatalf("count %d", l.count())
	}
	l.close()
	if !slices.Equal(log, []string{"+a", "+b", "+c", "-c", "-b", "-a"}) || l.count() != 0 {
		t.Fatalf("log %v count %d", log, l.count())
	}
	// Idempotent, and nothing can be added afterwards.
	l.close()
	reg("late")
	if len(log) != 6 || l.count() != 0 {
		t.Fatalf("a closed ledger took a registration or detached twice: %v", log)
	}
}

func TestListenersClearKeepsTheLedgerOpen(t *testing.T) {
	var l listeners
	n := 0
	l.add(func() { n++ }, func() { n-- })
	l.add(func() { n++ }, func() { n-- })
	l.clear()
	if n != 0 || l.count() != 0 {
		t.Fatalf("n=%d count=%d after clear", n, l.count())
	}
	l.add(func() { n++ }, func() { n-- })
	if n != 1 || l.count() != 1 {
		t.Fatal("clear must not close the ledger")
	}
}
