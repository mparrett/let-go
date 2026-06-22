package rt

import (
	"reflect"
	"testing"
)

// tokenize drains b fully into the sequence of keys nextKey produces.
func tokenize(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	b := []byte(s)
	for len(b) > 0 {
		k, n := nextKey(b)
		if n == 0 {
			t.Fatalf("nextKey made no progress on %q", b)
		}
		out = append(out, k)
		b = b[n:]
	}
	return out
}

func TestNextKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "l", []string{"l"}},
		{"burst splits", "llll", []string{"l", "l", "l", "l"}}, // the held-key fix
		{"arrow up", "\x1b[A", []string{"\x1b[A"}},
		{"two arrows", "\x1b[A\x1b[B", []string{"\x1b[A", "\x1b[B"}},
		{"modified arrow", "\x1b[1;2C", []string{"\x1b[1;2C"}},
		{"ss3 F1", "\x1bOP", []string{"\x1bOP"}},
		{"bare esc", "\x1b", []string{"\x1b"}},
		{"sgr mouse press", "\x1b[<0;10;5M", []string{"\x1b[<0;10;5M"}},
		{"sgr mouse release", "\x1b[<0;10;5m", []string{"\x1b[<0;10;5m"}},
		{"key mouse key", "l\x1b[<0;1;1Mx", []string{"l", "\x1b[<0;1;1M", "x"}},
		{"utf8 rune kept whole", "é", []string{"é"}},
		{"ctrl-c", "\x03", []string{"\x03"}},
		{"enter", "\r", []string{"\r"}},
		{"mixed printable + arrow + burst", "ab\x1b[Dcc", []string{"a", "b", "\x1b[D", "c", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tokenize(t, c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("tokens(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestNextKeyEmpty(t *testing.T) {
	if k, n := nextKey(nil); k != "" || n != 0 {
		t.Errorf("nextKey(nil) = (%q, %d), want (\"\", 0)", k, n)
	}
}

func TestIncompleteToken(t *testing.T) {
	// incompleteToken checks the FRONT token (the one ReadKey emits next), so
	// only a buffer whose leading token is split counts as incomplete.
	incomplete := []string{
		// escape sequences with no terminator yet
		"\x1b",         // lone ESC — may be a sequence split on the ESC byte
		"\x1b[",        // CSI with no params/final
		"\x1b[<0;10;5", // SGR mouse missing its final M/m
		"\x1b[1;",      // CSI mid-params
		"\x1bO",        // SS3 missing its third byte
		// multi-byte runes split at the front
		"\xc3",         // first byte of é (2-byte rune)
		"\xe2\x82",     // first two bytes of € (3-byte rune)
		"\xf0\x9f\x98", // first three bytes of 😀 (4-byte rune)
	}
	for _, s := range incomplete {
		if !incompleteToken([]byte(s)) {
			t.Errorf("incompleteToken(%q) = false, want true", s)
		}
	}
	complete := []string{
		"",              // empty
		"l",             // plain key
		"\x1b[A",        // finished arrow
		"\x1b[<0;10;5M", // finished SGR mouse
		"\x1bOP",        // finished SS3 (F1)
		"\x1bx",         // ESC + ordinary byte (Alt-x); nextKey splits it
		"é",             // complete 2-byte rune
		"€",             // complete 3-byte rune
		"😀",             // complete 4-byte rune
		// complete front token ahead of a split tail — emit the front first,
		// the tail is refilled once it reaches the front
		"abc\x1b[1;2", // 'a' is complete; trailing CSI handled later
		"aaa\xc3",     // 'a' is complete; the split é (reviewer's case) handled later
		"\xff",        // invalid lead byte — nextKey emits it as-is
	}
	for _, s := range complete {
		if incompleteToken([]byte(s)) {
			t.Errorf("incompleteToken(%q) = true, want false", s)
		}
	}
}
