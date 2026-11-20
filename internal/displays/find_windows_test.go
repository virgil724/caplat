//go:build windows

package displays

import (
	"strings"
	"testing"
)

func TestFindByIDAndNotFound(t *testing.T) {
	list := []Display{
		{Name: `\\.\DISPLAY3`, ID: `\\?\DISPLAY#MSI3CC0#1`, FriendlyName: "MAG274QRF-QD", Primary: true},
		{Name: `\\.\DISPLAY1`, ID: `\\?\DISPLAY#AVT0553#1`, FriendlyName: "AVT GC553", LikelyCard: true},
	}
	d, err := Find(list, `\\?\display#avt0553#1`)
	if err != nil || d.Name != `\\.\DISPLAY1` {
		t.Fatalf("by ID: %+v %v", d, err)
	}
	if d, err := Find(list, "DISPLAY3"); err != nil || !d.Primary {
		t.Fatalf("by name: %+v %v", d, err)
	}
	_, err = Find(list, `\\.\DISPLAY2`)
	if err == nil || !strings.Contains(err.Error(), "DISPLAY1 AVT GC553") || strings.Contains(err.Error(), `\\\\`) {
		t.Fatalf("not found error: %v", err)
	}
}
