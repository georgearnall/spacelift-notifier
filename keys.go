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
		buf := make([]byte, 3)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			k := decodeKey(buf[:n])
			if k == keyNone {
				continue
			}
			select {
			case ch <- k:
			case <-done:
				return
			}
		}
	}()
	return ch, restore
}

func decodeKey(b []byte) key {
	switch {
	case len(b) == 1 && (b[0] == 'q' || b[0] == 3): // 3 = Ctrl-C
		return keyQuit
	case len(b) == 1 && (b[0] == '\r' || b[0] == '\n'):
		return keyEnter
	case len(b) == 3 && b[0] == 0x1b && b[1] == '[' && b[2] == 'A':
		return keyUp
	case len(b) == 3 && b[0] == 0x1b && b[1] == '[' && b[2] == 'B':
		return keyDown
	default:
		return keyNone
	}
}
