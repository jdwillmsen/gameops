package sessionid

import "testing"

func TestBeginReplacesCurrent(t *testing.T) {
	first := Begin()
	if first == "" || Current() != first {
		t.Fatalf("Current() = %q after Begin() = %q", Current(), first)
	}
	if second := Begin(); second == first || Current() != second {
		t.Fatalf("second session %q reuses or does not replace %q", second, first)
	}
}
