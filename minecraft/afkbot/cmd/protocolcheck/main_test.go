package main

import "testing"

func TestDecide(t *testing.T) {
	tests := []struct {
		name                string
		library, production string
		vendored, upstream  string
		want                status
	}{
		{
			name:    "identical versions match",
			library: "1.26.45", production: "1.26.45",
			vendored: "v1.61.0", upstream: "v1.61.0",
			want: statusMatch,
		},
		{
			name:    "match wins even when a newer module exists",
			library: "1.26.45", production: "1.26.45",
			vendored: "v1.61.0", upstream: "v1.62.0",
			want: statusMatch,
		},
		{
			name:    "mismatch with a newer module upstream is ours to fix",
			library: "1.26.45", production: "1.26.51.1",
			vendored: "v1.61.0", upstream: "v1.62.0",
			want: statusBehind,
		},
		{
			name:    "mismatch on upstream's newest is not actionable",
			library: "1.26.45", production: "1.26.51.1",
			vendored: "v1.61.0", upstream: "v1.61.0",
			want: statusBlockedOnUpstream,
		},
		{
			name:    "unknown upstream falls back to behind",
			library: "1.26.45", production: "1.26.51.1",
			vendored: "v1.61.0", upstream: "",
			want: statusBehind,
		},
		{
			name:    "unreadable build info falls back to behind",
			library: "1.26.45", production: "1.26.51.1",
			vendored: "", upstream: "v1.61.0",
			want: statusBehind,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decide(tt.library, tt.production, tt.vendored, tt.upstream); got != tt.want {
				t.Errorf("decide(%q, %q, %q, %q) = %q, want %q",
					tt.library, tt.production, tt.vendored, tt.upstream, got, tt.want)
			}
		})
	}
}
