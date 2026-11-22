//go:build !windows && !linux

package displays

// List is only supported on Windows and Linux.
func List() ([]Display, error) {
	return nil, ErrUnsupported
}
