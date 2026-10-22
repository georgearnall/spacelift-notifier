package main

import "testing"

func TestDecodeKey(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want key
	}{
		{"q quits", []byte("q"), keyQuit},
		{"ctrl-c quits", []byte{3}, keyQuit},
		{"carriage return is enter", []byte("\r"), keyEnter},
		{"newline is enter", []byte("\n"), keyEnter},
		{"l relogins", []byte("l"), keyRelogin},
		{"a toggles all", []byte("a"), keyToggleAll},
		{"r refreshes", []byte("r"), keyRefresh},
		{"? toggles config", []byte("?"), keyConfig},
		{"up arrow", []byte{0x1b, '[', 'A'}, keyUp},
		{"down arrow", []byte{0x1b, '[', 'B'}, keyDown},
		{"unrecognized letter", []byte("x"), keyNone},
		{"unrecognized escape sequence", []byte{0x1b, '[', 'Z'}, keyNone},
		{"empty input", []byte{}, keyNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeKey(c.in); got != c.want {
				t.Errorf("decodeKey(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestReadKeys_NonTerminalNeverSends(t *testing.T) {
	// Under `go test`, stdin is not a terminal, so readKeys should return
	// a channel that never sends rather than blocking or panicking.
	done := make(chan struct{})
	defer close(done)

	ch, restore := readKeys(done)
	select {
	case k := <-ch:
		t.Fatalf("readKeys() sent %v on a non-terminal stdin, want no sends", k)
	default:
	}
	restore() // must be a safe no-op when stdin was never put into raw mode
}

func TestDecodeKeys_SplitsMultipleKeysInOneRead(t *testing.T) {
	in := []byte{0x1b, '[', 'B', 0x1b, '[', 'B', 'x', 0x1b, '[', 'Z', '\r', 'q'}
	want := []key{keyDown, keyDown, keyEnter, keyQuit}
	got := decodeKeys(in)
	if len(got) != len(want) {
		t.Fatalf("decodeKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("decodeKeys()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
