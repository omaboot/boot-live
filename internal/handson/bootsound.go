package handson

import (
	"sync"
	"sync/atomic"
	"time"
)

// BootSound plays one of Boot's sounds (internal/sounds) on the built-in output the way the
// audio tests play theirs: aplay on the codec's analog PCM, the mixer set to the tests'
// moderate level while it plays and put back after (the sounds themselves are quiet, about
// −24 dBFS). The tests' sounds come first: Boot's waits until theirs are done (the microphone
// test's encore starts as the test is decided, just when Boot's blip for it comes; 0.8.3 dev:
// the blip took the device and the encore didn't play), up to bootWait, else it's dropped; and
// a test's sound stops Boot's. Nil when io can't play sound or the Mac has no built-in output.
func BootSound(io IO, wav []byte) Player {
	p, ok := builtInPCM(io, false, "speaker")
	if !ok {
		return nil
	}
	s, ok := io.(*System)
	if !ok {
		if snd, ok := io.(soundIO); ok {
			return snd.Play(p, wav)
		}
		return nil
	}
	b := &bootPlayer{}
	go func() {
		for waited := time.Duration(0); ; waited += 50 * time.Millisecond {
			b.mu.Lock()
			if b.stopped || waited > bootWait {
				b.mu.Unlock()
				return
			}
			if testSounds.Load() == 0 {
				b.inner = s.play(p, wav, true)
				bootMu.Lock()
				bootNow = b.inner
				bootMu.Unlock()
				b.mu.Unlock()
				return
			}
			b.mu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return b
}

// bootWait is how long one of Boot's sounds waits for the tests' sounds to end (the encore is
// about 4 s).
const bootWait = 6 * time.Second

var (
	testSounds atomic.Int32 // the tests' sounds playing now (System.Play)
	bootMu     sync.Mutex
	bootNow    Player // Boot's sound playing now, or one that has ended
)

// stopBoot stops Boot's sound, if one is playing, and waits (up to a second) for its aplay to
// let go of the device.
func stopBoot() {
	bootMu.Lock()
	p := bootNow
	bootNow = nil
	bootMu.Unlock()
	pp, ok := p.(*procPlayer)
	if !ok {
		return
	}
	pp.Stop()
	select {
	case <-pp.exited:
	case <-time.After(time.Second):
	}
}

// bootPlayer is one of Boot's sounds, waiting for the device or playing.
type bootPlayer struct {
	mu      sync.Mutex
	stopped bool
	inner   Player
}

func (b *bootPlayer) Done() (error, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inner == nil {
		return nil, b.stopped
	}
	return b.inner.Done()
}

func (b *bootPlayer) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	if b.inner != nil {
		b.inner.Stop()
	}
}

func (b *bootPlayer) Setup() string { return "" }
