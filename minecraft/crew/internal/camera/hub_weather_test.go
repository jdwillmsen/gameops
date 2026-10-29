package camera

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func levels(t *testing.T, f []byte) (rain, thunder uint8) {
	t.Helper()
	if len(f) != 3 || f[0] != frameWeather {
		t.Fatalf("weather frame % x, want 3 bytes", f)
	}
	return f[1], f[2]
}

func TestWeatherFollowsLevelEventsAndReplaysToNewViewers(t *testing.T) {
	h, _ := newLocatedHub(t, 2)
	v, _ := h.Attach()
	if ms, _ := drain(t, v); len(ofType(ms, frameWeather)) != 0 {
		t.Fatal("clear weather sent a frame")
	}

	h.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartRaining, EventData: 65535}, &sink{})
	h.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartThunderstorm, EventData: 0}, &sink{})
	ms, _ := drain(t, v)
	f := ofType(ms, frameWeather)
	if len(f) != 2 {
		t.Fatalf("frames %v, want one per change", f)
	}
	if r, th := levels(t, f[1]); r != 255 || th != 1 {
		t.Fatalf("rain %d thunder %d, want 255 and the minimum for an intensity-less start", r, th)
	}

	late, _ := h.Attach()
	ms, _ = drain(t, late)
	if f := ofType(ms, frameWeather); len(f) != 1 {
		t.Fatalf("late viewer frames %v, want the current weather once", f)
	} else if r, th := levels(t, f[0]); r != 255 || th != 1 {
		t.Fatalf("late viewer rain %d thunder %d", r, th)
	}

	h.Apply(&packet.LevelEvent{EventType: packet.LevelEventStopRaining}, &sink{})
	h.Apply(&packet.LevelEvent{EventType: packet.LevelEventStopRaining}, &sink{})
	h.Apply(&packet.LevelEvent{EventType: packet.LevelEventStopThunderstorm}, &sink{})
	ms, _ = drain(t, v)
	f = ofType(ms, frameWeather)
	if len(f) != 2 {
		t.Fatalf("frames %v, want a repeated stop to be silent", f)
	}
	if r, th := levels(t, f[1]); r != 0 || th != 0 {
		t.Fatalf("rain %d thunder %d, want clear", r, th)
	}
}

func TestWeatherClearsWhenTheSessionEnds(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	h.SetWeather(.5, 0)
	h.Reset()
	v, _ := h.Attach()
	if ms, _ := drain(t, v); len(ofType(ms, frameWeather)) != 0 {
		t.Fatal("weather survived the session")
	}
}

func TestWeatherFrameIsIgnoredByTheV3Downgrade(t *testing.T) {
	d := &v3Downgrader{}
	f := encodeWeather(weather{rain: 9, thunder: 3})
	g, err := d.convert(f)
	if err != nil || string(g) != string(f) {
		t.Fatalf("convert = % x, %v; want the frame unchanged", g, err)
	}
}

func TestWeatherClearReachesAViewerAlreadyConnectedAtReset(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	v, _ := h.Attach()
	h.SetWeather(.5, 0)
	h.Reset()
	ms, _ := drain(t, v)
	f := ofType(ms, frameWeather)
	if len(f) == 0 {
		t.Fatal("a connected viewer was never told the rain stopped")
	}
	if r, th := levels(t, f[len(f)-1]); r != 0 || th != 0 {
		t.Fatalf("last weather rain %d thunder %d, want clear", r, th)
	}
}
