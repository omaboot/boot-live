package demo

import (
	"bytes"
	"os/exec"
	"sync"

	"github.com/omaboot/boot-live/internal/sounds"
)

// soundMu guards the sound playing now.
var (
	soundMu  sync.Mutex
	soundCmd *exec.Cmd
)

// Sound plays a sound through the computer's default ALSA device when the demo is Audible (the
// screenshots' demo never is), cutting off the one before it.
func (b *Backend) Sound(wav []byte) {
	if !b.Audible {
		return
	}
	if _, err := exec.LookPath("aplay"); err != nil {
		return
	}
	soundMu.Lock()
	defer soundMu.Unlock()
	if soundCmd != nil && soundCmd.Process != nil {
		soundCmd.Process.Kill()
	}
	c := exec.Command("aplay", "-q", "-")
	c.Stdin = bytes.NewReader(wav)
	if c.Start() == nil {
		soundCmd = c
		go c.Wait()
	}
}

// SaveSounds: the demo keeps nothing.
func (b *Backend) SaveSounds(sounds.Prefs) {}
