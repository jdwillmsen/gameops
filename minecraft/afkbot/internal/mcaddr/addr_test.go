package mcaddr

import "testing"

func TestIsNetherNet(t *testing.T) {
	for addr, want := range map[string]bool{
		"http://192.168.1.87:31134": true,
		"https://mc.example:443":    true,
		"HTTP://mc.example:31134":   true,
		"192.168.1.87:19132":        false,
		"mc.example":                false,
		"httpbin.example:19132":     false,
		"":                          false,
	} {
		if got := IsNetherNet(addr); got != want {
			t.Errorf("IsNetherNet(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	for host, want := range map[string]string{
		"mc.example":                "mc.example:25565",
		"::1":                       "[::1]:25565",
		"http://192.168.1.87":       "http://192.168.1.87:25565",
		"https://mc.example/":       "https://mc.example:25565",
		"http://192.168.1.87:31134": "http://192.168.1.87:31134",
		"http://mc.example:31134/":  "http://mc.example:31134",
		"HTTP://mc.example":         "http://mc.example:25565",
	} {
		got, err := Resolve(host, 25565)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", host, got, err, want)
		}
	}
}

func TestResolveRejectsWhatTheClientRefuses(t *testing.T) {
	for _, host := range []string{"http://", "http://:31134", "http://mc.example:31134/x", "http://u:p@mc.example:1", "http://mc.example?x=1", "http://[::1"} {
		if got, err := Resolve(host, 25565); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", host, got)
		}
	}
}

func TestURLPort(t *testing.T) {
	for host, want := range map[string]string{
		"http://mc.example:31134": "31134",
		"http://mc.example":       "",
		"mc.example":              "",
	} {
		if got := URLPort(host); got != want {
			t.Errorf("URLPort(%q) = %q, want %q", host, got, want)
		}
	}
}
