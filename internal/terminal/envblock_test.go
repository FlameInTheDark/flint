package terminal

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// decodeEnvBlock converts a UTF-16 environment block back to a string.
func decodeEnvBlock(t *testing.T, block []uint16) string {
	t.Helper()
	if len(block) < 2 {
		t.Fatalf("block too short: %v", block)
	}
	return string(utf16.Decode(block))
}

// TestEnvBlockUTF16Structure pins the CREATE_UNICODE_ENVIRONMENT block
// layout: "KEY=VALUE" entries separated by single NULs, two trailing NULs
// (the empty closing entry). The original bug: the block was built with
// syscall.UTF16FromString, which rejects embedded NUL bytes with EINVAL —
// Windows text "invalid argument" — so every Windows spawn died with a
// bare "invalid argument" notice before the shell ever started.
func TestEnvBlockUTF16Structure(t *testing.T) {
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor", "PROMPT=цена $"}
	block := envBlockUTF16(env)

	// Two trailing NULs: the block's empty closing entry.
	if block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		t.Fatalf("block must end with two NULs, got trailing %v", block[len(block)-3:])
	}

	decoded := decodeEnvBlock(t, block)
	// Round trip: the entries survive encoding intact.
	if got, want := decoded, strings.Join(env, "\x00")+"\x00\x00"; got != want {
		t.Fatalf("decoded block = %q, want %q", got, want)
	}

	// No double NUL before the end: an empty entry would terminate the
	// block early and truncate the child's environment.
	body := decoded[:len(decoded)-2]
	if strings.Contains(body, "\x00\x00") {
		t.Fatalf("empty entry inside the block would truncate it: %q", decoded)
	}
	if got := strings.Split(body, "\x00"); len(got) != len(env) {
		t.Fatalf("entry count = %d, want %d (%q)", len(got), len(env), decoded)
	}
}

// TestEnvBlockUTF16DropsEmptyEntries: an empty entry is the block
// terminator — it must be dropped, not encoded.
func TestEnvBlockUTF16DropsEmptyEntries(t *testing.T) {
	block := envBlockUTF16([]string{"A=1", "", "B=2"})
	if got, want := decodeEnvBlock(t, block), "A=1\x00B=2\x00\x00"; got != want {
		t.Fatalf("block = %q, want %q", got, want)
	}
}

// TestEnvBlockUTF16EmptyEnv: an empty environment is the minimal block.
func TestEnvBlockUTF16EmptyEnv(t *testing.T) {
	if got := envBlockUTF16(nil); len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("empty env block = %v, want [0 0]", got)
	}
}

// TestEnvBlockUTF16Astral: runes outside the BMP (surrogate pairs) survive
// the encode pass — the block is valid UTF-16 for non-ASCII values.
func TestEnvBlockUTF16Astral(t *testing.T) {
	block := envBlockUTF16([]string{"MSG=𝄞 clef"})
	if got, want := decodeEnvBlock(t, block), "MSG=𝄞 clef\x00\x00"; got != want {
		t.Fatalf("astral round trip = %q, want %q", got, want)
	}
}
