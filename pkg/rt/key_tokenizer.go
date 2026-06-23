package rt

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type keyScanResult int

const (
	keyReady keyScanResult = iota
	keyNeedMore
)

// scanKey classifies exactly one key token off the front of b and returns the
// token status plus the number of bytes to consume when the token is emitted.
//
// A single raw terminal read can carry several keys (held-key auto-repeat or
// queued input) or one multi-byte escape sequence (arrows, SGR mouse). The
// pre-tokenizer read-key returned the whole read as one string, so a burst
// like "llll" arrived as one unrecognized blob. Scanning lets read-key hand
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
// A token split across two reads returns keyNeedMore with the bytes currently
// held for that front token. ReadKey refills first when more bytes are pending;
// otherwise it emits those bytes best-effort rather than stalling forever on a
// genuinely truncated sequence.
func scanKey(b []byte) (keyScanResult, int) {
	if len(b) == 0 {
		return keyReady, 0
	}
	if b[0] == 0x1b { // ESC
		if len(b) == 1 {
			return keyNeedMore, 1
		}
		switch b[1] {
		case '[': // CSI: ESC [ params/intermediates final
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i < len(b) {
				return keyReady, i + 1 // include the final byte
			}
			return keyNeedMore, len(b)
		case 'O': // SS3: ESC O x
			if len(b) < 3 {
				return keyNeedMore, len(b)
			}
			return keyReady, 3
		}
		return keyReady, 1 // bare ESC
	}
	if b[0] < utf8.RuneSelf { // single-byte ASCII / control
		return keyReady, 1
	}
	if !utf8.FullRune(b) {
		return keyNeedMore, len(b)
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		return keyReady, 1 // invalid byte — emit as-is, never stall
	}
	return keyReady, size
}

// nextKey splits exactly one key token off the front of b and returns the token
// plus the number of bytes consumed. If b contains a partial front token, it
// emits the held bytes best-effort; ReadKey normally avoids that by refilling
// when scanKey reports keyNeedMore and stdin has more bytes pending.
func nextKey(b []byte) (string, int) {
	_, n := scanKey(b)
	if n == 0 {
		return "", 0
	}
	return string(b[:n]), n
}

// MouseEvent is a decoded SGR (1006) mouse report. Click-only scope: press and
// release of the three buttons plus wheel up/down, with Shift/Ctrl/Meta from the
// button byte. Drag/motion (the 1002/1003 modes) is out of scope, so the motion
// bit is ignored here — those modes are never enabled.
type MouseEvent struct {
	Action            string // "press" | "release"
	Button            string // "left" | "middle" | "right" | "wheel-up" | "wheel-down" | "none"
	X, Y              int    // 1-based cell coordinates
	Shift, Ctrl, Meta bool
}

// decodeSGRMouse parses one SGR (1006) mouse report:
//
//	ESC '[' '<' Cb ';' Cx ';' Cy ('M'|'m')   M = press, m = release
//
// Cb is the button byte: low two bits select the button, bit 2/3/4 carry
// Shift/Meta/Ctrl, bit 6 marks a wheel event. Returns the decoded event and
// true, or a zero event and false if s is not a well-formed SGR mouse report.
// nextKey keeps such a report intact as a single token, so this runs on exactly
// one report at a time.
func decodeSGRMouse(s string) (MouseEvent, bool) {
	if !strings.HasPrefix(s, "\x1b[<") || len(s) < 4 {
		return MouseEvent{}, false
	}
	final := s[len(s)-1]
	if final != 'M' && final != 'm' {
		return MouseEvent{}, false
	}
	parts := strings.Split(s[3:len(s)-1], ";")
	if len(parts) != 3 {
		return MouseEvent{}, false
	}
	cb, err1 := strconv.Atoi(parts[0])
	x, err2 := strconv.Atoi(parts[1])
	y, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return MouseEvent{}, false
	}

	ev := MouseEvent{
		X: x, Y: y,
		Shift: cb&4 != 0,
		Meta:  cb&8 != 0,
		Ctrl:  cb&16 != 0,
	}
	if final == 'M' {
		ev.Action = "press"
	} else {
		ev.Action = "release"
	}
	switch {
	case cb&64 != 0: // wheel
		if cb&1 == 0 {
			ev.Button = "wheel-up"
		} else {
			ev.Button = "wheel-down"
		}
	default:
		ev.Button = []string{"left", "middle", "right", "none"}[cb&3]
	}
	return ev, true
}
