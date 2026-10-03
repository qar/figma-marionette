package main

// Adding the plugin to Figma desktop, so nobody has to dig the manifest out of
// ~/Library in a file picker. Figma keeps development plugins in the
// undocumented "localFileExtensions" list of its settings.json: one entry for
// the manifest and one per code/UI file, linked by ids. Figma reads the file
// at startup and rewrites it on exit, so an edit only sticks while it is closed.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pluginID is the "id" in figma-plugin/manifest.json.
const pluginID = "9999000000000001"

func figmaSettingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Figma", "settings.json")
}

// figmaEntry is the part of a localFileExtensions entry Marionette reads.
type figmaEntry struct {
	ID                int    `json:"id"`
	ManifestPath      string `json:"manifestPath"`
	LastKnownPluginID string `json:"lastKnownPluginId"`
	FileMetadata      struct {
		Type           string `json:"type"`
		ManifestFileID int    `json:"manifestFileId"`
		CodeFileID     int    `json:"codeFileId"`
		UIFileIDs      []int  `json:"uiFileIds"`
	} `json:"fileMetadata"`
}

type figmaSettings struct {
	doc     []byte            // the file as read
	raw     []json.RawMessage // localFileExtensions entries, verbatim
	entries []figmaEntry      // the same entries, parsed
}

func readFigmaSettings(path string) (*figmaSettings, error) {
	doc, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(doc, &top); err != nil {
		return nil, fmt.Errorf("Figma's settings file is not valid JSON: %w", err)
	}
	s := &figmaSettings{doc: doc}
	if list, ok := top["localFileExtensions"]; ok {
		if err := json.Unmarshal(list, &s.raw); err != nil {
			return nil, fmt.Errorf("unexpected localFileExtensions in Figma's settings: %w", err)
		}
	}
	s.entries = make([]figmaEntry, len(s.raw))
	for i, r := range s.raw {
		if err := json.Unmarshal(r, &s.entries[i]); err != nil {
			return nil, fmt.Errorf("unexpected localFileExtensions entry in Figma's settings: %w", err)
		}
	}
	return s, nil
}

// registered reports whether Figma lists the plugin in pluginDir, with its
// code and UI files linked.
func (s *figmaSettings) registered(pluginDir string) bool {
	byID := map[int]figmaEntry{}
	for _, e := range s.entries {
		byID[e.ID] = e
	}
	for _, e := range s.entries {
		if e.FileMetadata.Type != "manifest" || e.ManifestPath != filepath.Join(pluginDir, "manifest.json") {
			continue
		}
		code, ok := byID[e.FileMetadata.CodeFileID]
		if !ok || code.ManifestPath != filepath.Join(pluginDir, "code.js") {
			return false
		}
		for _, id := range e.FileMetadata.UIFileIDs {
			if byID[id].ManifestPath == filepath.Join(pluginDir, "ui.html") {
				return true
			}
		}
	}
	return false
}

// stale reports whether a Marionette plugin is listed anywhere but pluginDir,
// e.g. the repo checkout the plugin used to be imported from.
func (s *figmaSettings) stale(pluginDir string) bool {
	for _, e := range s.entries {
		if e.FileMetadata.Type == "manifest" && e.LastKnownPluginID == pluginID && filepath.Dir(e.ManifestPath) != pluginDir {
			return true
		}
	}
	return false
}

func figmaRegistered(settingsPath, pluginDir string) bool {
	s, err := readFigmaSettings(settingsPath)
	return err == nil && s.registered(pluginDir)
}

// registerFigmaPlugin lists the plugin in pluginDir in Figma's settings and
// drops older Marionette entries. Everything else in the file is kept byte for
// byte, and the original is backed up next to it. It reports whether it wrote.
func registerFigmaPlugin(settingsPath, pluginDir string, now time.Time) (bool, error) {
	s, err := readFigmaSettings(settingsPath)
	if err != nil {
		return false, err
	}
	if s.registered(pluginDir) && !s.stale(pluginDir) {
		return false, nil
	}

	// Drop every Marionette manifest, plus the code/UI entries living in the
	// same folders (Figma reuses ids, so links between entries can't be trusted).
	dropDirs := map[string]bool{pluginDir: true}
	for _, e := range s.entries {
		if e.FileMetadata.Type == "manifest" && e.LastKnownPluginID == pluginID {
			dropDirs[filepath.Dir(e.ManifestPath)] = true
		}
	}
	next := 0
	var kept []json.RawMessage
	for i, e := range s.entries {
		next = max(next, e.ID, e.FileMetadata.ManifestFileID, e.FileMetadata.CodeFileID)
		for _, id := range e.FileMetadata.UIFileIDs {
			next = max(next, id)
		}
		if !dropDirs[filepath.Dir(e.ManifestPath)] {
			kept = append(kept, s.raw[i])
		}
	}

	manifestID, codeID, uiID := next+1, next+2, next+3
	type link struct {
		Type           string `json:"type"`
		ManifestFileID int    `json:"manifestFileId"`
	}
	added := []any{
		struct {
			ID                   int    `json:"id"`
			ManifestPath         string `json:"manifestPath"`
			LastKnownName        string `json:"lastKnownName"`
			LastKnownPluginID    string `json:"lastKnownPluginId"`
			FileMetadata         any    `json:"fileMetadata"`
			CachedContainsWidget bool   `json:"cachedContainsWidget"`
		}{manifestID, filepath.Join(pluginDir, "manifest.json"), "Marionette", pluginID, struct {
			Type       string `json:"type"`
			CodeFileID int    `json:"codeFileId"`
			UIFileIDs  []int  `json:"uiFileIds"`
		}{"manifest", codeID, []int{uiID}}, false},
		struct {
			ID           int    `json:"id"`
			ManifestPath string `json:"manifestPath"`
			FileMetadata link   `json:"fileMetadata"`
		}{codeID, filepath.Join(pluginDir, "code.js"), link{"code", manifestID}},
		struct {
			ID           int    `json:"id"`
			ManifestPath string `json:"manifestPath"`
			FileMetadata link   `json:"fileMetadata"`
		}{uiID, filepath.Join(pluginDir, "ui.html"), link{"ui", manifestID}},
	}
	for _, a := range added {
		b, err := marshalCompact(a)
		if err != nil {
			return false, err
		}
		kept = append(kept, b)
	}
	list, err := marshalCompact(kept)
	if err != nil {
		return false, err
	}
	doc, err := spliceJSONKey(s.doc, "localFileExtensions", list)
	if err != nil {
		return false, err
	}
	if !json.Valid(doc) {
		return false, errors.New("refusing to write an invalid Figma settings file")
	}

	info, err := os.Stat(settingsPath)
	if err != nil {
		return false, err
	}
	backup := settingsPath + ".marionette-" + now.Format("20060102-150405") + ".bak"
	if err := os.WriteFile(backup, s.doc, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("backing up Figma's settings: %w", err)
	}
	tmp := settingsPath + ".marionette-tmp"
	if err := os.WriteFile(tmp, doc, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("writing Figma's settings: %w", err)
	}
	if err := os.Rename(tmp, settingsPath); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("writing Figma's settings: %w", err)
	}
	return true, nil
}

// spliceJSONKey replaces the value of a top-level key in a JSON object, or
// appends the key, leaving every other byte of doc untouched.
func spliceJSONKey(doc []byte, key string, value []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	keys := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keyEnd := int(dec.InputOffset())
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		keys++
		if tok != key {
			continue
		}
		valEnd := int(dec.InputOffset())
		start := keyEnd + bytes.IndexByte(doc[keyEnd:], ':') + 1
		for start < valEnd && strings.ContainsRune(" \t\r\n", rune(doc[start])) {
			start++
		}
		if !bytes.Equal(doc[start:valEnd], v) {
			return nil, errors.New("could not locate the value to replace")
		}
		return concat(doc[:start], value, doc[valEnd:]), nil
	}
	end := bytes.LastIndexByte(doc, '}')
	k, _ := json.Marshal(key)
	sep := []byte(",")
	if keys == 0 {
		sep = nil
	}
	return concat(doc[:end], sep, k, []byte(":"), value, doc[end:]), nil
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// figmaRunning reports whether Figma desktop is open.
func figmaRunning() bool {
	switch goos {
	case "darwin":
		return command("pgrep", "-x", "Figma").Run() == nil
	case "windows":
		out, _ := command("tasklist", "/FI", "IMAGENAME eq Figma.exe", "/NH").Output()
		return bytes.Contains(bytes.ToLower(out), []byte("figma.exe"))
	}
	return false
}

// quitFigma asks Figma to quit the way closing it would (Electron treats
// SIGTERM as a normal quit), so it saves its state on the way out.
func quitFigma() error {
	switch goos {
	case "darwin":
		return command("pkill", "-TERM", "-x", "Figma").Run()
	case "windows":
		return command("taskkill", "/IM", "Figma.exe").Run()
	}
	return errors.New("not supported on this system")
}

func launchFigma() error {
	switch goos {
	case "darwin":
		return command("open", "-a", "Figma").Run()
	case "windows":
		return command(filepath.Join(os.Getenv("LOCALAPPDATA"), "Figma", "Figma.exe")).Start()
	}
	return errors.New("not supported on this system")
}
