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
func readKeys(done <-chan struct{}) <-chan key {
	ch := make(chan key)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ch
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return ch
	}

	go func() {
		defer term.Restore(fd, oldState)
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
	return ch
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
