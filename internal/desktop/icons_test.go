package desktop

import "testing"

func TestIconsParse(t *testing.T) {
	if iconSettings == nil {
		t.Fatal("settings icon failed to parse")
	}
}
