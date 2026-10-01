package schedule

import (
	"testing"
	"time"
)

func at(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", "2026-10-01 "+hhmm)
	if err != nil {
		panic(err)
	}
	return t
}

func TestQuiet(t *testing.T) {
	ws, err := ParseQuiet("03:50-05:10, 05:30-06:10,23:50-00:20")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"03:49": false, "03:50": true, "04:00": true, "05:09": true, "05:10": false,
		"05:29": false, "05:30": true, "06:09": true, "06:10": false,
		// A window that crosses midnight covers both sides of it.
		"23:49": false, "23:50": true, "23:59": true, "00:00": true, "00:19": true, "00:20": false,
		"12:00": false,
	}
	for hhmm, want := range cases {
		if got := ws.Contains(at(hhmm)); got != want {
			t.Errorf("%s quiet = %v, want %v", hhmm, got, want)
		}
	}
}

// The windows are written in UTC because the jobs they protect are scheduled
// in UTC; a time in another zone is the same instant, not the same clock.
func TestQuiet_ComparesInUTC(t *testing.T) {
	ws, _ := ParseQuiet("04:00-05:00")
	chicago := time.FixedZone("CDT", -5*3600)
	if !ws.Contains(time.Date(2026, 9, 30, 23, 30, 0, 0, chicago)) {
		t.Error("23:30 CDT is 04:30 UTC and should be quiet")
	}
}

func TestParseQuiet(t *testing.T) {
	if ws, err := ParseQuiet(""); err != nil || len(ws) != 0 {
		t.Errorf("empty = %v, %v; want no windows", ws, err)
	}
	if ws, _ := ParseQuiet(""); ws.Contains(at("04:00")) {
		t.Error("no windows must never be quiet")
	}
	for _, bad := range []string{"04:00", "04:00-", "4-5", "24:00-25:00", "04:60-05:00", "04:00-04:00", "a:b-c:d", "04:00-05:00,"} {
		if _, err := ParseQuiet(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
