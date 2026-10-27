package main

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcdial"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/resource"
)

const testManifest = `{"format_version":2,"header":{"name":"p","description":"p","uuid":"c8b7905a-b5e3-45a6-a10e-488507337eea","version":[0,0,1],"min_engine_version":[1,21,0]},"modules":[{"type":"resources","uuid":"61f537ef-6abb-419b-8373-12023ef0ad60","version":[0,0,1]}]}`

// A world that requires a resource pack must not hold a unit's login: the unit
// declines the pack and still reaches the spawn.
func TestUnitDialDeclinesRequiredResourcePack(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("manifest.json")
	_, _ = w.Write([]byte(testManifest))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	pack, err := resource.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}

	l, err := minecraft.ListenConfig{
		AuthenticationDisabled: true,
		ResourcePacks:          []*resource.Pack{pack},
		TexturePacksRequired:   true,
	}.Listen("raknet", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		_ = c.(*minecraft.Conn).StartGame(minecraft.GameData{})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, leave, err := mcdial.DialWithLeave(ctx, minecraft.Dialer{
		IdentityData:         login.IdentityData{DisplayName: "Agate"},
		DownloadResourcePack: declinePack,
	}, l.Addr().String(), 5*time.Second, 10*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer leave(time.Second)
	if n := len(conn.ResourcePacks()); n != 0 {
		t.Errorf("unit downloaded %d packs, want none", n)
	}
}
