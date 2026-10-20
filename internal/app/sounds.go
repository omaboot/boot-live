package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sounds"
)

// Boot's sounds (internal/sounds): played on the Mac's own output, and the settings kept on the
// stick (sounds.File) for the next boot. The screens decide what plays when.

// soundState is the sound playing now and the settings waiting to be saved.
type soundState struct {
	mu      sync.Mutex
	playing handson.Player
	want    *sounds.Prefs // the newest settings not saved yet
	saving  bool
}

// Sound plays a sound, cutting off the one before it if it's still playing.
func (b *Backend) Sound(wav []byte) {
	if b.R.Fixture() || b.R.Gone() { // no aplay without the stick
		return
	}
	s := &b.snd
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playing != nil {
		s.playing.Stop()
	}
	s.playing = handson.BootSound(b.system(), wav)
}

// SaveSounds keeps the settings on the stick, in the background; settings changed while a save
// is under way are saved after it (the newest only). Saved as a report is (send.SaveBytes): a
// freshly written stick has no OMABOOT partition yet, and it's made for the first save (the
// Air's 0.8.3 run: M pressed with none there, the setting lost; the partition came at Finish).
func (b *Backend) SaveSounds(p sounds.Prefs) {
	r := b.R
	if r.Fixture() && r.Opt.StickDir == "" {
		return
	}
	s := &b.snd
	s.mu.Lock()
	s.want = &p
	if s.saving {
		s.mu.Unlock()
		return
	}
	s.saving = true
	s.mu.Unlock()
	go func() {
		for {
			s.mu.Lock()
			w := s.want
			s.want = nil
			if w == nil || r.Gone() {
				s.saving = false
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second) // a report save's: making the partition takes a while
			send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, w.JSON(), sounds.File, "")
			cancel()
		}
	}()
}

// loadSounds reads the settings the last boot kept; none: off, the defaults.
func (r *Run) loadSounds() {
	if r.Fixture() && r.Opt.StickDir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
		if b, err := os.ReadFile(filepath.Join(base, sounds.File)); err == nil {
			r.sounds = sounds.Parse(b)
		}
		return nil
	})
}
