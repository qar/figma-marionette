package main

// Setup helpers behind the buttons in the status window.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	marionette "github.com/qar/figma-marionette"
)

func appDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Marionette")
	return dir, os.MkdirAll(dir, 0o755)
}

// loadToken returns this install's plugin token, creating it on first run.
// Only the extracted plugin knows it, so web pages cannot pose as the plugin.
func loadToken(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "token")
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); len(tok) >= 32 {
			return tok, nil
		}
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating plugin token: %w", err)
	}
	tok := hex.EncodeToString(b[:])
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		return "", fmt.Errorf("saving plugin token: %w", err)
	}
	return tok, nil
}

// extractPlugin writes the embedded Figma plugin to a stable folder on every
// start, so the copy Figma imported always matches this app version, port
// and token.
func extractPlugin(dataDir string, port int, token string) (string, error) {
	dir := filepath.Join(dataDir, "figma-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating plugin folder: %w", err)
	}
	err := fs.WalkDir(marionette.FigmaPlugin, "figma-plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := marionette.FigmaPlugin.ReadFile(path)
		if err != nil {
			return err
		}
		s := strings.ReplaceAll(string(data), "127.0.0.1:3055", "127.0.0.1:"+strconv.Itoa(port))
		s = strings.ReplaceAll(s, "__MARIONETTE_TOKEN__", token)
		// The files carry the token; keep them private even if they predate this.
		dest := filepath.Join(dir, filepath.Base(path))
		if err := os.WriteFile(dest, []byte(s), 0o600); err != nil {
			return err
		}
		return os.Chmod(dest, 0o600)
	})
	if err != nil {
		return "", fmt.Errorf("writing plugin files: %w", err)
	}
	return dir, nil
}

func skillPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "skills", "figma-marionette", "SKILL.md")
}

// actions are bound into the window as JS functions.
type actions struct {
	pluginDir string
	updates   *updater

	mu         sync.Mutex
	figmaState string // "", "working", "added", or "error: ..."
}

// addToFigma adds the plugin to Figma desktop. While Figma is open it only
// answers "figma-running", unless restart is set: then Marionette quits Figma,
// edits its settings and reopens it. That runs in the background, since a
// bound function blocks the window; poll figmaProgress for the outcome.
func (a *actions) addToFigma(restart bool) (string, error) {
	settings := figmaSettingsPath()
	if _, err := os.Stat(settings); err != nil {
		return "", errors.New("Figma desktop was not found on this computer")
	}
	running := figmaRunning()
	if running && !restart {
		return "figma-running", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.figmaState != "working" {
		a.figmaState = "working"
		go func() {
			state := "added"
			if err := a.registerWithFigma(settings, running); err != nil {
				state = "error: " + err.Error()
			}
			a.mu.Lock()
			a.figmaState = state
			a.mu.Unlock()
		}()
	}
	return "working", nil
}

func (a *actions) figmaProgress() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.figmaState
}

func (a *actions) registerWithFigma(settings string, reopen bool) error {
	if reopen {
		if err := quitFigma(); err != nil {
			return fmt.Errorf("quitting Figma: %w", err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for figmaRunning() {
			if time.Now().After(deadline) {
				return errors.New("Figma did not quit. Save your work, quit Figma yourself, then try again")
			}
			time.Sleep(300 * time.Millisecond)
		}
		time.Sleep(time.Second) // let Figma's last settings write land
	}
	if _, err := registerFigmaPlugin(settings, a.pluginDir, time.Now()); err != nil {
		return err
	}
	if reopen {
		return launchFigma()
	}
	return nil
}

func (a *actions) revealPlugin() error {
	manifest := filepath.Join(a.pluginDir, "manifest.json")
	switch goos {
	case "darwin":
		return exec.Command("open", "-R", manifest).Run()
	case "windows":
		// explorer exits 1 even on success.
		exec.Command("explorer", "/select,", manifest).Run()
		return nil
	default:
		return errors.New("not supported on this system")
	}
}

func (a *actions) installSkill() (string, error) {
	path := skillPath()
	if path == "" {
		return "", errors.New("cannot locate the home directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating skill folder: %w", err)
	}
	if err := os.WriteFile(path, marionette.Skill, 0o644); err != nil {
		return "", fmt.Errorf("writing skill: %w", err)
	}
	return path, nil
}

// refreshSkill rewrites an installed skill that an earlier version of the app
// put there, so agents learn about what this version can do. A symlink is
// someone's own arrangement (a repo checkout, say) and is left alone.
func refreshSkill(path string) error {
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	old, err := os.ReadFile(path)
	if err != nil || bytes.Equal(old, marionette.Skill) {
		return nil
	}
	return os.WriteFile(path, marionette.Skill, 0o644)
}

// checkUpdates and installUpdate run in the background, since a bound
// function blocks the window; /status reports how they went.
func (a *actions) checkUpdates() {
	go a.updates.check()
}

func (a *actions) installUpdate() {
	go a.updates.install()
}

func (a *actions) openURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("only https links can be opened")
	}
	return openExternal(url)
}

// copyText backs up navigator.clipboard, which the window tries first.
func (a *actions) copyText(text string) error {
	if goos != "darwin" {
		return errors.New("copying is not supported here")
	}
	cmd := exec.Command("pbcopy")
	// Launched from Finder there is no locale, and pbcopy would assume MacRoman.
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func openExternal(url string) error {
	switch goos {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return errors.New("not supported on this system")
	}
}
