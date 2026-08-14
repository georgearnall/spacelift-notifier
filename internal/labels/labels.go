// Package labels implements the exact-match filtering that decides whether
// a Spacelift stack belongs to "your team", based on its label list.
package labels

// Config holds the set of labels that mark a stack as belonging to the
// caller's team. A stack matches if any of its labels exactly equals any
// entry in Labels.
type Config struct {
	Labels []string
}

// Match reports whether any label in labels exactly matches one of the
// configured team labels. When it matches, matchedOn is the configured
// label value that matched, which is useful for explaining to the user why
// a given stack was surfaced.
func Match(stackLabels []string, cfg Config) (matched bool, matchedOn string) {
	for _, l := range stackLabels {
		for _, want := range cfg.Labels {
			if l == want {
				return true, want
			}
		}
	}
	return false, ""
}
