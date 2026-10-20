package main

import (
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/ui"
)

// The audio and camera tests' screens. The demo's Fake has a fixed seed, so the counts and the
// word are the same every time: left 5 beeps (answer 4), right 2 (answer 1), the word "radio"
// (answer 0); headphones the same counts once Ready (answer 0) is picked; display-out 5 beeps.
// After the counts (or words) come "Play it again" (5, or 4 for the words) and "I hear nothing"
// (6, or 5); "Skip this one" is -1. The headphones then play 3 beeps in both ears (10.4 s to
// about 12.8 s) and ask whether the speakers stayed quiet (0 yes, 1 no). The microphone hears
// its five notes (D5, the pause, E5, C5, C4, G4: about 2.7 s a round from 1.9 s) in the demo;
// with alsa:lowcut 300 the speakers don't reach C4; without the loopback (noLoop) it asks for
// claps at 4.4 s.
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			playShot("h10-speakers-left", "audio.speakers", 2.5, nil),
			playShot("h10-speakers-ask", "audio.speakers", 4.2, nil),
			playShot("h10-speakers-right", "audio.speakers", 5.8, nil, ui.Pick{At: 4.4, Action: 4}),
			playShot("h10-speakers-word", "audio.speakers", 10.4, nil, ui.Pick{At: 4.4, Action: 4}, ui.Pick{At: 7, Action: 1}),
			playShot("h10-speakers-word-ask", "audio.speakers", 11.1, nil, ui.Pick{At: 4.4, Action: 4}, ui.Pick{At: 7, Action: 1}),
			playShot("h10-speakers-pass", "audio.speakers", 12.2, nil, ui.Pick{At: 4.4, Action: 4}, ui.Pick{At: 7, Action: 1}, ui.Pick{At: 11.3, Action: 0}),
			playShot("h10-speakers-retry", "audio.speakers", 5, nil, ui.Pick{At: 4.4, Action: 2}),
			variantShot("h10-speakers-unplug", "audio.speakers", 2, func(f *handson.Fake) { f.Set("alsa:card0:Headphone Jack", "on") }),
			playShot("h10-speakers-silent", "audio.speakers", 7.5, nil, ui.Pick{At: 4.4, Action: 6}, ui.Pick{At: 7, Action: 6}),
			playShot("h10-speakers-word-skipped", "audio.speakers", 12.2, nil, ui.Pick{At: 4.4, Action: 4}, ui.Pick{At: 7, Action: 1}, ui.Pick{At: 11.3, Action: -1}),

			playShot("h11-mic-room", "audio.microphone", 1.2, nil),
			playShot("h11-mic-tone", "audio.microphone", 3, nil),
			playShot("h11-mic-pause", "audio.microphone", 5.6, nil),
			playShot("h11-mic-tone-2", "audio.microphone", 8.2, nil),
			playShot("h11-mic-tone-3", "audio.microphone", 10.9, nil),
			playShot("h11-mic-tone-c4", "audio.microphone", 13.6, nil),
			playShot("h11-mic-tone-g4", "audio.microphone", 16.4, nil),
			playShot("h11-mic-pass", "audio.microphone", 18.4, nil),
			variantShot("h11-mic-c4-unheard", "audio.microphone", 16.4, func(f *handson.Fake) { f.Set("alsa:lowcut", "300") }),
			variantShot("h11-mic-c4-pass", "audio.microphone", 24, func(f *handson.Fake) { f.Set("alsa:lowcut", "300") }),
			variantShot("h11-mic-pause-heard", "audio.microphone", 10, func(f *handson.Fake) { f.Set("alsa:hold", "1") }),
			variantShot("h11-mic-unplug", "audio.microphone", 2.5, func(f *handson.Fake) { f.Set("alsa:card0:Headphone Jack", "on") }),
			variantShot("h11-mic-claps", "audio.microphone", 6, noLoop),
			variantShot("h11-mic-claps-pass", "audio.microphone", 8.6, func(f *handson.Fake) {
				noLoop(f)
				f.Set("alsa:claps", "5.5 5.9") // the demo's seed asks for 2
			}),
			variantShot("h11-mic-ask", "audio.microphone", 22, noLoop),
			variantShot("h11-mic-flat", "audio.microphone", 22, func(f *handson.Fake) { f.Set("alsa:mic", "flat") }),

			playShot("h12-camera-opening", "camera.builtin", 0.2, nil),
			playShot("h12-camera-preview", "camera.builtin", 1.6, nil),
			playShot("h12-camera-ask", "camera.builtin", 4, nil),
			playShot("h12-camera-pass", "camera.builtin", 5, nil, ui.Pick{At: 4.2, Action: 0}),
			variantShot("h12-camera-facetimehd", "camera.builtin", 1.5, func(f *handson.Fake) {
				for p := range f.Files {
					if len(p) > 22 && p[:22] == "/sys/class/video4linux" {
						delete(f.Files, p)
					}
				}
				f.Set("/sys/bus/pci/devices/0000:02:00.0/vendor", "0x14e4")
				f.Set("/sys/bus/pci/devices/0000:02:00.0/device", "0x1570")
			}),

			playShot("h13-display-audio-waiting", "audio.display-out", 1, nil),
			playShot("h13-display-audio-playing", "audio.display-out", 4, nil),
			playShot("h13-display-audio-ask", "audio.display-out", 6.8, nil),
			playShot("h13-display-audio-pass", "audio.display-out", 8, nil, ui.Pick{At: 7, Action: 4}),
			variantShot("h13-display-audio-no-eld", "audio.display-out", 13, func(f *handson.Fake) {
				f.Set("/proc/asound/card0/eld#3.0", "monitor_present\t\t1\neld_valid\t\t0")
			}),

			playShot("h14-headphones-plug", "audio.headphone", 2, nil),
			playShot("h14-headphones-ready", "audio.headphone", 3.8, nil),
			playShot("h14-headphones-left", "audio.headphone", 5.5, nil, ui.Pick{At: 4, Action: 0}),
			playShot("h14-headphones-ask", "audio.headphone", 7.4, nil, ui.Pick{At: 4, Action: 0}),
			playShot("h14-headphones-speakers-playing", "audio.headphone", 11.8, nil, ui.Pick{At: 4, Action: 0}, ui.Pick{At: 7.4, Action: 4}, ui.Pick{At: 10.4, Action: 1}),
			playShot("h14-headphones-speakers", "audio.headphone", 13.2, nil, ui.Pick{At: 4, Action: 0}, ui.Pick{At: 7.4, Action: 4}, ui.Pick{At: 10.4, Action: 1}),
			playShot("h14-headphones-pass", "audio.headphone", 14, nil, ui.Pick{At: 4, Action: 0}, ui.Pick{At: 7.4, Action: 4}, ui.Pick{At: 10.4, Action: 1}, ui.Pick{At: 13.2, Action: 0}),
			playShot("h14-headphones-speakers-played", "audio.headphone", 14, nil, ui.Pick{At: 4, Action: 0}, ui.Pick{At: 7.4, Action: 4}, ui.Pick{At: 10.4, Action: 1}, ui.Pick{At: 13.2, Action: 1}),
			playShot("h14-headphones-skipped", "audio.headphone", 2, nil, ui.Pick{At: 1.5, Action: -1}),
			variantShot("h14-headphones-no-jack", "audio.headphone", 9, func(*handson.Fake) {}),
		}
	})
}

// variantShot is a test's screen on a variant of the demo Mac, for the paths the demo itself
// doesn't take: the test's Demo script as it stands at 0 s (its timed changes dropped), then
// script's changes.
func variantShot(name, id string, t float64, script func(*handson.Fake), picks ...ui.Pick) shot {
	setup := demo.HandsOnSetup()
	io := setup.IO
	setup.IO = func(s handson.Spec) handson.IO {
		f := io(s).(*handson.Fake)
		if s.ID == id {
			f2 := handson.NewFake()
			handson.DemoMac(f2)
			s.Demo(f2)
			f2.Advance(0)
			f = handson.NewFake()
			for k, v := range f2.Files {
				f.Files[k] = v
			}
			script(f)
		}
		return f
	}
	h := ui.NewHandsOn(setup)
	i := -1
	for k, s := range h.Setup.Plan {
		if s.ID == id {
			i = k
		}
	}
	if i < 0 {
		panic("no hands-on test with a Demo script: " + id)
	}
	h.Play(i, t, picks...)
	s := st(ui.Test, t, wifiUp, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	return shot{name: name, st: s, f: f}
}

// noLoop is the demo Mac with a microphone that doesn't hear the speakers.
func noLoop(f *handson.Fake) { delete(f.Files, "alsa:loop") }
