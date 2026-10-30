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

func TestValidateNetherNet(t *testing.T) {
	for addr, ok := range map[string]bool{
		"http://192.168.1.87:31134": true,
		"https://mc.example:443":    true,
		"http://mc.example:31134/":  false,
		"http://mc.example":         false,
		"http://:31134":             false,
		"http://mc.example:31134/x": false,
		"http://u:p@mc.example:1":   false,
		"http://[::1":               false,
	} {
		if err := ValidateNetherNet(addr); (err == nil) != ok {
			t.Errorf("ValidateNetherNet(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}
