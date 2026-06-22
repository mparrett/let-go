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

func TestScanKeyNeedMore(t *testing.T) {
	// scanKey checks the FRONT token (the one ReadKey emits next), so only a
	// buffer whose leading token is split asks for more bytes.
	needMore := []string{
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
	for _, s := range needMore {
		status, n := scanKey([]byte(s))
		if status != keyNeedMore || n != len(s) {
			t.Errorf("scanKey(%q) = (%v, %d), want (%v, %d)", s, status, n, keyNeedMore, len(s))
		}
	}
}

func TestScanKeyReady(t *testing.T) {
	ready := []struct {
		in string
		n  int
	}{
		{"", 0},                                 // empty
		{"l", 1},                                // plain key
		{"\x1b[A", 3},                           // finished arrow
		{"\x1b[<0;10;5M", len("\x1b[<0;10;5M")}, // finished SGR mouse
		{"\x1bOP", 3},                           // finished SS3 (F1)
		{"\x1bx", 1},                            // ESC + ordinary byte (Alt-x); nextKey splits it
		{"é", len("é")},                         // complete 2-byte rune
		{"€", len("€")},                         // complete 3-byte rune
		{"😀", len("😀")},                         // complete 4-byte rune
		// complete front token ahead of a split tail — emit the front first,
		// the tail is refilled once it reaches the front
		{"abc\x1b[1;2", 1}, // 'a' is complete; trailing CSI handled later
		{"aaa\xc3", 1},     // 'a' is complete; the split é (reviewer's case) handled later
		{"\xff", 1},        // invalid lead byte — nextKey emits it as-is
	}
	for _, c := range ready {
		status, n := scanKey([]byte(c.in))
		if status != keyReady || n != c.n {
			t.Errorf("scanKey(%q) = (%v, %d), want (%v, %d)", c.in, status, n, keyReady, c.n)
		}
	}
}

func TestDecodeSGRMouse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want MouseEvent
	}{
		{"left press", "\x1b[<0;10;5M", MouseEvent{Action: "press", Button: "left", X: 10, Y: 5}},
		{"left release", "\x1b[<0;10;5m", MouseEvent{Action: "release", Button: "left", X: 10, Y: 5}},
		{"middle press", "\x1b[<1;3;4M", MouseEvent{Action: "press", Button: "middle", X: 3, Y: 4}},
		{"right press", "\x1b[<2;7;8M", MouseEvent{Action: "press", Button: "right", X: 7, Y: 8}},
		{"shift+left", "\x1b[<4;1;1M", MouseEvent{Action: "press", Button: "left", X: 1, Y: 1, Shift: true}},
		{"meta+left", "\x1b[<8;1;1M", MouseEvent{Action: "press", Button: "left", X: 1, Y: 1, Meta: true}},
		{"ctrl+left", "\x1b[<16;1;1M", MouseEvent{Action: "press", Button: "left", X: 1, Y: 1, Ctrl: true}},
		{"ctrl+shift+right", "\x1b[<22;2;2M", MouseEvent{Action: "press", Button: "right", X: 2, Y: 2, Shift: true, Ctrl: true}},
		{"wheel up", "\x1b[<64;9;9M", MouseEvent{Action: "press", Button: "wheel-up", X: 9, Y: 9}},
		{"wheel down", "\x1b[<65;9;9M", MouseEvent{Action: "press", Button: "wheel-down", X: 9, Y: 9}},
		{"large coords", "\x1b[<0;220;140M", MouseEvent{Action: "press", Button: "left", X: 220, Y: 140}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := decodeSGRMouse(c.in)
			if !ok {
				t.Fatalf("decodeSGRMouse(%q) returned ok=false", c.in)
			}
			if got != c.want {
				t.Errorf("decodeSGRMouse(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestDecodeSGRMouseRejects(t *testing.T) {
	// Non-mouse tokens and malformed reports must report ok=false so read-key
	// falls through to returning them as plain key strings.
	for _, in := range []string{
		"l",             // plain key
		"\x1b[A",        // arrow (CSI, no '<')
		"\x1b[<0;10;5",  // missing final M/m
		"\x1b[<0;10M",   // only two params
		"\x1b[<x;10;5M", // non-numeric param
		"\x1b[<",        // truncated
	} {
		if ev, ok := decodeSGRMouse(in); ok {
			t.Errorf("decodeSGRMouse(%q) = %+v, ok=true; want ok=false", in, ev)
		}
	}
}
