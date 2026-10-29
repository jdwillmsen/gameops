package camera

import "github.com/sandertv/gophertunnel/minecraft/protocol/packet"

// weatherScale is the intensity the server sends for a full storm in a level
// event's data.
const weatherScale = 65535

type weather struct{ rain, thunder uint8 }

func weatherLevel(f float32) uint8 {
	switch {
	case f != f || f <= 0:
		return 0
	case f >= 1:
		return 255
	}
	return uint8(f*255 + .5)
}

// SetWeather gives the hub the rain and thunder levels (0 to 1) from the
// server's game data.
func (h *Hub) SetWeather(rain, thunder float32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setWeatherLocked(weather{weatherLevel(rain), weatherLevel(thunder)})
}

func (h *Hub) setWeatherLocked(w weather) {
	if h.weather == w {
		return
	}
	h.weather = w
	h.pushOrderedLocked(encodeWeather(w))
}

func (h *Hub) levelEvent(p *packet.LevelEvent) {
	var (
		level  = func() uint8 { return weatherLevel(float32(p.EventData) / weatherScale) }
		change func(*weather)
	)
	switch p.EventType {
	case packet.LevelEventStartRaining:
		// A start with no intensity still means it is raining.
		change = func(w *weather) { w.rain = max(level(), 1) }
	case packet.LevelEventStartThunderstorm:
		change = func(w *weather) { w.thunder = max(level(), 1) }
	case packet.LevelEventStopRaining:
		change = func(w *weather) { w.rain = 0 }
	case packet.LevelEventStopThunderstorm:
		change = func(w *weather) { w.thunder = 0 }
	default:
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	w := h.weather
	change(&w)
	h.setWeatherLocked(w)
}
