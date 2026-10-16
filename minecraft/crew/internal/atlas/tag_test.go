package atlas

import "testing"

func TestSelectTag(t *testing.T) {
	tags := []string{
		"v1.26.50.4", "v1.26.40.05", "v1.26.30.5", "v1.26.60.29-preview",
		"v1.26.0.2", "not-a-version", "v1.21.130.3",
	}
	tests := []struct {
		name    string
		server  string
		want    string
		wantErr bool
	}{
		{"exact match", "1.26.50.4", "v1.26.50.4", false},
		{"hotfix without tag falls back to lower tag", "1.26.52.3", "v1.26.50.4", false},
		{"zero padded tag component compares numerically", "1.26.40.9", "v1.26.40.05", false},
		{"preview tags are never chosen", "1.26.60.30", "v1.26.50.4", false},
		{"three component server version", "1.26.30", "v1.26.0.2", false},
		{"no tag at or below", "1.20.0.1", "", true},
		{"unparseable server version", "latest", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectTag(tc.server, tags)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("tag = %q, want %q", got, tc.want)
			}
		})
	}
}
