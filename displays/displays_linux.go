//go:build linux

package displays

// List returns the enabled DRM outputs attached to the system.
func List() ([]Display, error) {
	return listLinux("/sys/class/drm", openDRMDevice)
}
