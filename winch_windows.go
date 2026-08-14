//go:build windows

package main

// watchResize is a no-op on Windows: there is no SIGWINCH there, and
// nothing in this tool's layout depends on terminal width.
func watchResize(done <-chan struct{}) <-chan struct{} {
	return make(chan struct{})
}
