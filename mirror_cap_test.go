// SPDX-License-Identifier: BSD-3-Clause

package webcanvas

import (
	"strconv"
	"testing"
)

func TestIsActivationKey(t *testing.T) {
	for k, want := range map[string]bool{"Enter": true, " ": true, "a": false, "Tab": false, "": false, "Space": false} {
		if got := isActivationKey(k); got != want {
			t.Errorf("isActivationKey(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestIsKeyboardEcho(t *testing.T) {
	for _, c := range []struct {
		detail int
		keyAt  float64
		now    float64
		want   bool
	}{
		{0, 1000, 1001, true}, {0, 1000, 1499, true}, {0, 1000, 1500, false}, {0, 1000, 5000, false},
		{1, 1000, 1001, false}, {0, 0, 1, false}, {0, -1, 1, false},
	} {
		if got := isKeyboardEcho(c.detail, c.keyAt, c.now); got != c.want {
			t.Errorf("isKeyboardEcho(%d, %v, %v) = %v, want %v", c.detail, c.keyAt, c.now, got, c.want)
		}
	}
}

func many(n int) []A11yNode {
	ns := make([]A11yNode, n)
	for i := range ns {
		ns[i] = A11yNode{Key: "k" + strconv.Itoa(i), Role: "button", Name: strconv.Itoa(i)}
	}
	return ns
}

func TestCapNodes(t *testing.T) {
	t.Run("at or under the cap is untouched", func(t *testing.T) {
		for _, n := range []int{0, 1, 10} {
			ns := many(n)
			out, cut := capNodes(ns, 10)
			if cut || len(out) != n {
				t.Errorf("%d nodes: cut=%v len=%d", n, cut, len(out))
			}
		}
	})
	t.Run("above the cap: exactly max, status first, first nodes in order", func(t *testing.T) {
		out, cut := capNodes(many(100), 10)
		if !cut || len(out) != 10 {
			t.Fatalf("cut=%v len=%d", cut, len(out))
		}
		if out[0].Key != truncatedKey || out[0].Role != "status" || out[0].Name != "Accessibility view truncated: showing 9 of 100 items" {
			t.Fatalf("status node %+v", out[0])
		}
		for i := 1; i < 10; i++ {
			if want := "k" + strconv.Itoa(i-1); out[i].Key != want {
				t.Errorf("out[%d] = %q, want %q", i, out[i].Key, want)
			}
		}
	})
	t.Run("the focused node and its subtree survive, in order", func(t *testing.T) {
		ns := many(100)
		ns[90].Focused = true
		ns[90].Key = "Group#0"
		for _, i := range []int{91, 92} {
			ns[i].Key = "Group#0/Button#" + strconv.Itoa(i)
		}
		out, _ := capNodes(ns, 10)
		var keys []string
		for _, n := range out {
			keys = append(keys, n.Key)
		}
		// 9 slots: focus + 2 subtree, then the first 6 in order; kept in original order.
		if len(keys) != 10 {
			t.Fatalf("len %d: %q", len(keys), keys)
		}
		got := map[string]bool{}
		for _, k := range keys {
			got[k] = true
		}
		for _, k := range []string{"Group#0", "Group#0/Button#91", "Group#0/Button#92"} {
			if !got[k] {
				t.Errorf("%q cut: %q", k, keys)
			}
		}
		if keys[len(keys)-3] != "Group#0" {
			t.Errorf("order: %q", keys)
		}
	})
	t.Run("a huge subtree takes at most half the budget", func(t *testing.T) {
		ns := many(100)
		ns[50].Focused, ns[50].Key = true, "G"
		for i := 51; i < 100; i++ {
			ns[i].Key = "G/c" + strconv.Itoa(i)
		}
		out, _ := capNodes(ns, 11) // budget 10, subtree limit 6 (focus + 5)
		sub, head := 0, 0
		for _, n := range out[1:] {
			if n.Key == "G" || len(n.Key) > 2 && n.Key[:2] == "G/" {
				sub++
			} else {
				head++
			}
		}
		if sub != 6 || head != 4 || len(out) != 11 {
			t.Errorf("subtree %d head %d len %d", sub, head, len(out))
		}
	})
	t.Run("a hostile tree stays bounded", func(t *testing.T) {
		out, cut := capNodes(many(200000), maxMirrorNodes)
		if !cut || len(out) != maxMirrorNodes {
			t.Fatalf("cut=%v len=%d", cut, len(out))
		}
	})
}
