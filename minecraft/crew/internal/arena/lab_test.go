package arena

import "testing"

func TestCheckLabAllowlist(t *testing.T) {
	for _, tc := range []struct {
		lab, confirm string
		ok           bool
	}{
		{"", "", true},
		{"lab", "", true},
		{KnownLab, "", true},
		{"crew-01", "", false},
		{"crew-01", "crew-01", true},
		{"k8s:other/pod", "", false},
		{"k8s:other/pod", "k8s:another/pod", false},
		{"k8s:other/pod", "k8s:other/pod", true},
	} {
		if err := CheckLab(tc.lab, tc.confirm); (err == nil) != tc.ok {
			t.Errorf("CheckLab(%q, %q) = %v, want ok=%v", tc.lab, tc.confirm, err, tc.ok)
		}
	}
}

func TestNewLabConsoleRejectsABadPodTarget(t *testing.T) {
	for _, lab := range []string{"k8s:", "k8s:ns", "k8s:/pod", "k8s:ns/"} {
		if _, err := NewLabConsole(lab, "compose.yaml"); err == nil {
			t.Errorf("%q accepted", lab)
		}
	}
	if _, err := NewLabConsole("k8s:ns/pod", ""); err != nil {
		t.Error(err)
	}
	if _, err := NewLabConsole("lab", "compose.yaml"); err != nil {
		t.Error(err)
	}
}
