package rt

import "unicode/utf8"

// nextKey splits exactly one key token off the front of b and returns the
// token plus the number of bytes consumed.
//
// A single raw terminal read can carry several keys (held-key auto-repeat or
// queued input) or one multi-byte escape sequence (arrows, SGR mouse). The
// pre-tokenizer read-key returned the whole read as one string, so a burst
// like "llll" arrived as one unrecognized blob. Tokenizing lets read-key hand
// out one event per call: "llll" -> four "l", while "\x1b[C" stays one key.
//
// Recognized fronts:
//
//	ESC '[' … final(0x40-0x7E)   one CSI sequence (arrows, modified keys, SGR mouse)
//	ESC 'O' x                    one SS3 sequence (application-cursor keys, F1-F4)
//	ESC (alone / other)          the bare Escape key
//	UTF-8 lead byte              the whole rune (never split)
//	any other byte               that single byte (ASCII / control)
//
// A token split across two reads (an unterminated CSI, or a multi-byte rune
// missing continuation bytes) is returned best-effort as the remaining buffer.
// ReadKey avoids hitting that path: it checks incompleteToken and refills first
// when more bytes are pending, so nextKey runs on a complete token.
func nextKey(b []byte) (string, int) {
	if len(b) == 0 {
		return "", 0
	}
	if b[0] == 0x1b { // ESC
		if len(b) >= 2 && b[1] == '[' { // CSI: ESC [ params/intermediates final
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i < len(b) {
				return string(b[:i+1]), i + 1 // include the final byte
			}
			return string(b), len(b) // incomplete — best effort
		}
		if len(b) >= 3 && b[1] == 'O' { // SS3: ESC O x
			return string(b[:3]), 3
		}
		return "\x1b", 1 // bare ESC
	}
	if b[0] < utf8.RuneSelf { // single-byte ASCII / control
		return string(b[:1]), 1
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		return string(b[:1]), 1 // invalid byte — emit as-is, never stall
	}
	return string(b[:size]), size
}

// incompleteToken reports whether b ends in the middle of a token whose
// remaining bytes haven't arrived yet — an escape sequence with no terminator,
// or a multi-byte UTF-8 rune missing continuation bytes. A raw read fills a
// fixed-size buffer, so a token can straddle two reads (and queued input makes
// this routine, not a corner case: a burst longer than the buffer, or several
// multi-byte reports back to back, will split). When this is true and more
// bytes are actually pending, ReadKey refills before tokenizing rather than
// letting nextKey emit a broken partial. Read-buffer size then only affects how
// often the refill path runs, not correctness.
func incompleteToken(b []byte) bool {
	return incompleteEscape(b) || incompleteRune(b)
}

// incompleteEscape reports whether b holds the opening of an escape sequence
// whose terminating byte hasn't arrived: a lone ESC, a CSI (ESC '[' …) with no
// final byte in 0x40–0x7E, or an SS3 (ESC 'O') missing its third byte.
//
// A lone ESC is ambiguous (the Escape key, or the start of a sequence), which
// normally needs a timeout to resolve. ReadKey gates the refill on bytes
// actually waiting: a real Escape keypress has nothing queued behind it, while
// a split sequence's tail is already in the kernel buffer. That sidesteps the
// timeout for the common case, and over-pulling is harmless — nextKey still
// splits a bare ESC off the front correctly.
func incompleteEscape(b []byte) bool {
	if len(b) == 0 || b[0] != 0x1b {
		return false
	}
	if len(b) == 1 {
		return true // lone ESC — may be a sequence split on the ESC byte
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				return false // final byte present — sequence is complete
			}
		}
		return true // only params/intermediates so far
	case 'O':
		return len(b) < 3
	}
	return false
}

// incompleteRune reports whether b *starts* with a UTF-8 lead byte whose
// continuation bytes haven't all arrived — the front rune is split across a
// read. Like incompleteEscape, this checks the front token (the one ReadKey is
// about to emit), not the tail: a complete byte ahead of a split rune is
// emitted first, and the split rune is refilled once it reaches the front (its
// pending tail waits in the kernel buffer until then). ASCII, a continuation
// byte, or an invalid lead at the front is "complete" — nextKey emits it as-is,
// so refilling would only stall.
func incompleteRune(b []byte) bool {
	if len(b) == 0 || b[0] < utf8.RuneSelf {
		return false // empty or ASCII — front is complete
	}
	var need int
	switch {
	case b[0]&0xe0 == 0xc0: // 110xxxxx
		need = 2
	case b[0]&0xf0 == 0xe0: // 1110xxxx
		need = 3
	case b[0]&0xf8 == 0xf0: // 11110xxx
		need = 4
	default:
		return false // continuation byte or invalid lead — nextKey emits as-is
	}
	return len(b) < need
}
