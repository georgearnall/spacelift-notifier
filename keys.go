package main

import (
	"os"

	"golang.org/x/term"
)

// key represents a single decoded keypress relevant to this tool.
type key int

const (
	keyNone key = iota
	keyQuit
	keyUp
	keyDown
	keyEnter
	keyRelogin
	keyToggleAll
)

// readKeys puts stdin into raw mode (if it's a terminal) and sends
// decoded keypresses on the returned channel until done is closed or
// stdin is closed. If stdin isn't a terminal (e.g. input is redirected),
// it returns a channel that never sends, and the watch loop simply runs
// on its poll timer with no keyboard interaction.
//
// It also returns a restore function that puts the terminal back the way
// it found it. The caller MUST defer this at the top level of whatever
// function put the terminal into raw mode, rather than relying on the
// reader goroutine to restore it itself: that goroutine spends nearly
// all its life blocked inside os.Stdin.Read, which has no way to be
// interrupted by closing done, so a restore left only in the goroutine's
// own defer would in practice never run - leaving the tty (and every
// other program run in that terminal afterward) stuck in raw mode.
func readKeys(done <-chan struct{}) (<-chan key, func()) {
	ch := make(chan key)
	noop := func() {}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ch, noop
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return ch, noop
	}
	restore := func() { term.Restore(fd, oldState) }

	go func() {
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			for _, k := range decodeKeys(buf[:n]) {
				select {
				case ch <- k:
				case <-done:
					return
				}
			}
		}
	}()
	return ch, restore
}

// reenterRawMode puts stdin back into raw mode after a caller has
// temporarily left it (e.g. via the restore func returned by readKeys) to
// run an interactive subprocess. Safe to call even if stdin isn't a
// terminal; the error is ignored the same way readKeys ignores it when
// first entering raw mode.
func reenterRawMode() {
	term.MakeRaw(int(os.Stdin.Fd()))
}

// decodeKeys splits one read's worth of input into keypresses and decodes
// each, dropping unrecognized ones. A single read can hold several keys
// when they're typed or auto-repeated quickly (e.g. two arrow presses
// arrive as one 6-byte read).
func decodeKeys(b []byte) []key {
	var keys []key
	for len(b) > 0 {
		n := 1
		if b[0] == 0x1b && len(b) >= 3 && b[1] == '[' {
			n = 3 // CSI sequence such as an arrow key
		}
		if k := decodeKey(b[:n]); k != keyNone {
			keys = append(keys, k)
		}
		b = b[n:]
	}
	return keys
}

func decodeKey(b []byte) key {
	switch {
	case len(b) == 1 && (b[0] == 'q' || b[0] == 3): // 3 = Ctrl-C
		return keyQuit
	case len(b) == 1 && (b[0] == '\r' || b[0] == '\n'):
		return keyEnter
	case len(b) == 1 && b[0] == 'l':
		return keyRelogin
	case len(b) == 1 && b[0] == 'a':
		return keyToggleAll
	case len(b) == 3 && b[0] == 0x1b && b[1] == '[' && b[2] == 'A':
		return keyUp
	case len(b) == 3 && b[0] == 0x1b && b[1] == '[' && b[2] == 'B':
		return keyDown
	default:
		return keyNone
	}
}
