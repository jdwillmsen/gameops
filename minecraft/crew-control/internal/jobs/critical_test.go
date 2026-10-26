package jobs

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func ps(key string, sec int, deps ...string) PathStep {
	return PathStep{Key: key, DependsOn: deps, Duration: time.Duration(sec) * time.Second, Known: true}
}

func TestCriticalPath(t *testing.T) {
	unknown := PathStep{Key: "u", DependsOn: []string{"a"}}
	cases := []struct {
		name   string
		steps  []PathStep
		path   []string
		length time.Duration
		slack  map[string]time.Duration
	}{
		{"chain", []PathStep{ps("a", 2), ps("b", 3, "a"), ps("c", 4, "b")},
			[]string{"a", "b", "c"}, 9 * time.Second, map[string]time.Duration{"a": 0, "b": 0, "c": 0}},
		{"diamond takes the longer arm", []PathStep{ps("a", 1), ps("b", 5, "a"), ps("c", 2, "a"), ps("d", 1, "b", "c")},
			[]string{"a", "b", "d"}, 7 * time.Second, map[string]time.Duration{"a": 0, "b": 0, "c": 3 * time.Second, "d": 0}},
		{"parallel branches of equal length: smaller key wins, both have no slack",
			[]PathStep{ps("x", 4), ps("y", 4), ps("z", 1, "x", "y")},
			[]string{"x", "z"}, 5 * time.Second, map[string]time.Duration{"x": 0, "y": 0, "z": 0}},
		{"independent steps: the longest is the path", []PathStep{ps("a", 3), ps("b", 9)},
			[]string{"b"}, 9 * time.Second, map[string]time.Duration{"a": 6 * time.Second, "b": 0}},
		{"unknown duration is never critical and does not lengthen the job",
			[]PathStep{ps("a", 2), unknown, ps("b", 3, "a")},
			[]string{"a", "b"}, 5 * time.Second, map[string]time.Duration{"a": 0, "b": 0}},
		{"empty", nil, nil, 0, map[string]time.Duration{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CriticalPath(c.steps)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Path, c.path) || got.Length != c.length {
				t.Fatalf("path %v length %v, want %v %v", got.Path, got.Length, c.path, c.length)
			}
			for k, want := range c.slack {
				if got.Steps[k].Slack != want {
					t.Errorf("slack[%s] = %v, want %v", k, got.Steps[k].Slack, want)
				}
			}
			for _, s := range c.steps {
				if got.Steps[s.Key].Critical != slices.Contains(c.path, s.Key) {
					t.Errorf("critical[%s] mismatch", s.Key)
				}
			}
		})
	}
}

func TestCriticalPathFlagsUnknown(t *testing.T) {
	got, _ := CriticalPath([]PathStep{ps("a", 2), {Key: "u", DependsOn: []string{"a"}}})
	if !got.Steps["u"].Unknown || got.Steps["u"].Critical || got.Steps["a"].Unknown {
		t.Fatalf("steps = %+v", got.Steps)
	}
}

func TestCriticalPathRejects(t *testing.T) {
	cases := []struct {
		name  string
		steps []PathStep
		want  error
	}{
		{"cycle", []PathStep{ps("a", 1, "b"), ps("b", 1, "a")}, ErrCycle},
		{"self loop", []PathStep{ps("a", 1, "a")}, ErrCycle},
		{"unknown dependency", []PathStep{ps("a", 1, "nope")}, ErrInvalid},
		{"duplicate key", []PathStep{ps("a", 1), ps("a", 2)}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := CriticalPath(c.steps); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}
