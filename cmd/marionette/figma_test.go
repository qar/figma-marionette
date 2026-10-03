package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpliceJSONKey(t *testing.T) {
	cases := []struct {
		name, doc, want string
	}{
		{"middle key, spacing kept", `{"a":1, "b" : [1,2], "c":3}`, `{"a":1, "b" : [9], "c":3}`},
		{"last key", `{"a":1,"b":{"x":[1]}}`, `{"a":1,"b":[9]}`},
		{"missing key", `{"a":1}`, `{"a":1,"b":[9]}`},
		{"empty object", `{}`, `{"b":[9]}`},
		{"nested key of the same name is left alone", `{"a":{"b":1},"b":2}`, `{"a":{"b":1},"b":[9]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := spliceJSONKey([]byte(c.doc), "b", []byte(`[9]`))
			if err != nil || string(got) != c.want {
				t.Errorf("got %s, %v; want %s", got, err, c.want)
			}
		})
	}
}

// A settings file shaped like a real one: an unrelated plugin, the plugin as
// once imported from a repo checkout, a stray entry from that checkout, and
// a link to an id Figma has since reused.
const figmaFixture = `{"locale":"en","localFileExtensions":[` +
	`{"id":2,"manifestPath":"/other/code.js","fileMetadata":{"type":"code","manifestFileId":1}},` +
	`{"id":1,"manifestPath":"/repo/manifest.json","lastKnownName":"Figma Agent Bridge","lastKnownPluginId":"9999000000000001","fileMetadata":{"type":"manifest","codeFileId":4,"uiFileIds":[7]},"cachedContainsWidget":false},` +
	`{"id":4,"manifestPath":"/repo/code.js","fileMetadata":{"type":"code","manifestFileId":1}},` +
	`{"id":7,"manifestPath":"/repo/ui.html","fileMetadata":{"type":"ui","manifestFileId":1}},` +
	`{"id":14,"manifestPath":"/repo/ui.html","fileMetadata":{"type":"ui","manifestFileId":13}},` +
	`{"id":5,"manifestPath":"/kept/manifest.json","lastKnownName":"Kept","lastKnownPluginId":"123","fileMetadata":{"type":"manifest","codeFileId":6,"uiFileIds":[20]}},` +
	`{"id":6,"manifestPath":"/kept/code.js","fileMetadata":{"type":"code","manifestFileId":5}}` +
	`],"zoomStop":2}`

func TestRegisterFigmaPlugin(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(figmaFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginDir := "/data/Marionette/figma-plugin"
	now := time.Date(2026, 10, 3, 16, 5, 1, 0, time.UTC)

	changed, err := registerFigmaPlugin(settings, pluginDir, now)
	if err != nil || !changed {
		t.Fatalf("register: changed=%v err=%v", changed, err)
	}
	doc, _ := os.ReadFile(settings)
	s, err := readFigmaSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !s.registered(pluginDir) || s.stale(pluginDir) {
		t.Errorf("registered=%v stale=%v; want true, false\n%s", s.registered(pluginDir), s.stale(pluginDir), doc)
	}
	for _, e := range s.entries {
		if strings.HasPrefix(e.ManifestPath, "/repo/") {
			t.Errorf("old checkout entry survived: %+v", e)
		}
		if strings.HasPrefix(e.ManifestPath, pluginDir) && e.ID <= 20 {
			t.Errorf("new entry id %d collides with ids already in use (max 20)", e.ID)
		}
	}
	for _, kept := range []string{
		`{"id":2,"manifestPath":"/other/code.js","fileMetadata":{"type":"code","manifestFileId":1}}`,
		`{"id":5,"manifestPath":"/kept/manifest.json","lastKnownName":"Kept","lastKnownPluginId":"123","fileMetadata":{"type":"manifest","codeFileId":6,"uiFileIds":[20]}}`,
	} {
		if !bytes.Contains(doc, []byte(kept)) {
			t.Errorf("unrelated entry not kept verbatim: %s", kept)
		}
	}
	if !bytes.HasPrefix(doc, []byte(`{"locale":"en","localFileExtensions":[`)) || !bytes.HasSuffix(doc, []byte(`],"zoomStop":2}`)) {
		t.Errorf("bytes outside localFileExtensions changed:\n%s", doc)
	}
	if backup, err := os.ReadFile(settings + ".marionette-20261003-160501.bak"); err != nil || string(backup) != figmaFixture {
		t.Errorf("backup missing or wrong: %v", err)
	}

	changed, err = registerFigmaPlugin(settings, pluginDir, now.Add(time.Minute))
	if err != nil || changed {
		t.Errorf("second register: changed=%v err=%v; want a no-op", changed, err)
	}
	if again, _ := os.ReadFile(settings); !bytes.Equal(again, doc) {
		t.Error("second register rewrote the file")
	}
}

func TestRegisterFigmaPluginWithoutList(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(settings, []byte(`{"locale":"en"}`), 0o644)
	if _, err := registerFigmaPlugin(settings, "/p", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !figmaRegistered(settings, "/p") {
		doc, _ := os.ReadFile(settings)
		t.Fatalf("not registered:\n%s", doc)
	}
}

func TestRegisterFigmaPluginRejectsBrokenSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(settings, []byte(`{"localFileExtensions":`), 0o644)
	if _, err := registerFigmaPlugin(settings, "/p", time.Now()); err == nil {
		t.Fatal("wrote into an unreadable settings file")
	}
	if doc, _ := os.ReadFile(settings); string(doc) != `{"localFileExtensions":` {
		t.Fatal("broken settings file was modified")
	}
}
