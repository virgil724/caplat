// Package wasapi captures audio in-process with WASAPI (shared mode,
// event-driven), with the driver's capture time of every packet on the
// QueryPerformanceCounter clock. Low-latency shared mode (IAudioClient3)
// is used when the driver offers an engine period below the default 10 ms.
package wasapi

import (
	"fmt"
	"strings"
)

// FindName returns the one name equal to want, or else the one containing
// it (case-insensitive).
func FindName(names []string, want string) (string, error) {
	var hits []string
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return n, nil
		}
		if strings.Contains(strings.ToLower(n), strings.ToLower(want)) {
			hits = append(hits, n)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no audio endpoint matches %q; have %q", want, names)
	}
	return "", fmt.Errorf("%q matches several endpoints: %q", want, hits)
}
