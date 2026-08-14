//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// watchResize returns a channel that receives a value shortly after the
// terminal is resized (SIGWINCH), debounced so a burst of resize events
// (common while dragging a window edge) collapses into a single redraw.
func watchResize(done <-chan struct{}) <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	out := make(chan struct{}, 1)

	go func() {
		defer signal.Stop(sig)
		const debounce = 100 * time.Millisecond
		var timer *time.Timer
		var timerC <-chan time.Time
		for {
			select {
			case <-sig:
				if timer == nil {
					timer = time.NewTimer(debounce)
				} else {
					timer.Reset(debounce)
				}
				timerC = timer.C
			case <-timerC:
				select {
				case out <- struct{}{}:
				default:
				}
				timerC = nil
			case <-done:
				return
			}
		}
	}()
	return out
}
