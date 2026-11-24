package terminal

import (
	"strings"
	"unicode/utf16"
)

// envBlockUTF16 builds the UTF-16 environment block CreateProcessW expects
// together with CREATE_UNICODE_ENVIRONMENT: "KEY=VALUE" entries, each
// NUL-terminated, the block closed by an empty entry (two trailing NULs).
//
// syscall.UTF16FromString must NOT be used for the joined block: it rejects
// any string containing a NUL byte with EINVAL — whose Windows error text
// is exactly "invalid argument" — so encoding the entries as one rune slice
// and converting in a single utf16.Encode pass is the only way to keep the
// NUL separators. Empty entries are dropped: an empty entry terminates the
// block early and would silently truncate the environment for the child.
func envBlockUTF16(env []string) []uint16 {
	entries := make([]string, 0, len(env))
	for _, kv := range env {
		if kv != "" {
			entries = append(entries, kv)
		}
	}
	if len(entries) == 0 {
		return []uint16{0, 0}
	}
	return utf16.Encode([]rune(strings.Join(entries, "\x00") + "\x00\x00"))
}
