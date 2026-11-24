package terminal

// EncodeKey converts a keystroke into the byte sequence a real terminal
// sends to the shell (xterm encoding; DECCKM-aware for the cursor keys).
// An empty result means "nothing to send" (unhandled modifier combo).
//
// This is the terminal's input contract: unlike the old line-based model,
// every keystroke reaches the shell raw — the shell does its own line
// editing, history and completion, and Ctrl+C arrives as 0x03 so the
// line discipline delivers SIGINT to the foreground process.
func (t *Terminal) EncodeKey(key, keyChar string, ctrl, alt, shift bool) []byte {
	// Ctrl+letter / punctuation → the matching control byte (the layout
	// xterm uses; digits 2-8 follow xterm's ctrl+<digit> map).
	if ctrl && !alt {
		if b, ok := ctrlKeyBytes[key]; ok {
			return b
		}
		return nil // ctrl+digits and unknown combos are not shell keys
	}

	prefix := func(b []byte) []byte {
		if alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	// Special keys (named by the keymap, no KeyChar).
	switch key {
	case "enter":
		return prefix([]byte("\r"))
	case "backspace":
		return prefix([]byte{0x7f})
	case "tab":
		if shift {
			return prefix([]byte("\x1b[Z"))
		}
		return prefix([]byte("\t"))
	case "escape":
		return prefix([]byte{0x1b})
	case "up":
		return t.cursorKey(alt, 'A')
	case "down":
		return t.cursorKey(alt, 'B')
	case "right":
		return t.cursorKey(alt, 'C')
	case "left":
		return t.cursorKey(alt, 'D')
	case "home":
		return prefix([]byte("\x1b[H"))
	case "end":
		return prefix([]byte("\x1b[F"))
	case "pageup":
		return prefix([]byte("\x1b[5~"))
	case "pagedown":
		return prefix([]byte("\x1b[6~"))
	case "delete":
		return prefix([]byte("\x1b[3~"))
	case "insert":
		return prefix([]byte("\x1b[2~"))
	case "f1":
		return prefix([]byte("\x1bOP"))
	case "f2":
		return prefix([]byte("\x1bOQ"))
	case "f3":
		return prefix([]byte("\x1bOR"))
	case "f4":
		return prefix([]byte("\x1bOS"))
	case "f5":
		return prefix([]byte("\x1b[15~"))
	case "f6":
		return prefix([]byte("\x1b[17~"))
	case "f7":
		return prefix([]byte("\x1b[18~"))
	case "f8":
		return prefix([]byte("\x1b[19~"))
	case "f9":
		return prefix([]byte("\x1b[20~"))
	case "f10":
		return prefix([]byte("\x1b[21~"))
	case "f11":
		return prefix([]byte("\x1b[23~"))
	case "f12":
		return prefix([]byte("\x1b[24~"))
	}

	// Printable characters: UTF-8 bytes (with Shift the platform already
	// uppercased the KeyChar).
	if keyChar != "" {
		return prefix([]byte(keyChar))
	}
	return nil
}

// ctrlKeyBytes is the control-byte table: ctrl+<key> → byte.
var ctrlKeyBytes = map[string][]byte{
	"a": {0x01}, "A": {0x01},
	"b": {0x02}, "B": {0x02},
	"c": {0x03}, "C": {0x03},
	"d": {0x04}, "D": {0x04},
	"e": {0x05}, "E": {0x05},
	"f": {0x06}, "F": {0x06},
	"g": {0x07}, "G": {0x07},
	"h": {0x08}, "H": {0x08},
	"i": {0x09}, "I": {0x09}, "tab": {0x09},
	"j": {0x0a}, "J": {0x0a}, "enter": {0x0a},
	"k": {0x0b}, "K": {0x0b},
	"l": {0x0c}, "L": {0x0c},
	"m": {0x0d}, "M": {0x0d},
	"n": {0x0e}, "N": {0x0e},
	"o": {0x0f}, "O": {0x0f},
	"p": {0x10}, "P": {0x10},
	"q": {0x11}, "Q": {0x11},
	"r": {0x12}, "R": {0x12},
	"s": {0x13}, "S": {0x13},
	"t": {0x14}, "T": {0x14},
	"u": {0x15}, "U": {0x15},
	"v": {0x16}, "V": {0x16},
	"w": {0x17}, "W": {0x17},
	"x": {0x18}, "X": {0x18},
	"y": {0x19}, "Y": {0x19},
	"z": {0x1a}, "Z": {0x1a},
	"space": {0x00},
	"@":     {0x00}, "`": {0x00}, "2": {0x00},
	"3": {0x1b}, "[": {0x1b},
	"4": {0x1c}, "\\": {0x1c},
	"5": {0x1d}, "]": {0x1d},
	"6": {0x1e}, "^": {0x1e},
	"7": {0x1f}, "/": {0x1f}, "_": {0x1f},
	"8": {0x7f},
}

// cursorKey encodes the arrows: CSI A/B/C/D normally, SS3 O-prefixed in
// application cursor-key mode (DECCKM, set by full-screen programs).
func (t *Terminal) cursorKey(alt bool, final byte) []byte {
	t.mu.Lock()
	app := t.emu != nil && t.emu.appCursor
	t.mu.Unlock()
	seq := []byte{'\x1b', '[', final}
	if app {
		seq = []byte{'\x1b', 'O', final}
	}
	if alt {
		return append([]byte{0x1b}, seq...)
	}
	return seq
}
