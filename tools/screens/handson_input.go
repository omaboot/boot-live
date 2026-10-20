package main

import "github.com/omaboot/boot-live/internal/ui"

// The keyboard, trackpad and Force Touch screens, played on the demo's scripted Mac (each test's
// Demo script in internal/handson/input_*.go).
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			playShot("h10-keyboard-start", "input.keyboard", 0.8, nil),
			playShot("h10-keyboard-typing", "input.keyboard", 13.4, nil),
			playShot("h10-keyboard-nearly", "input.keyboard", 27, nil),
			playShot("h10-keyboard-letgo", "input.keyboard", 30.78, nil),
			playShot("h10-keyboard-answers", "input.keyboard", 32.5, nil),
			playShot("h10-keyboard-partial", "input.keyboard", 33.5, nil, ui.Pick{At: 32.6, Action: 1}),
			playShot("h10-keyboard-pass", "input.keyboard", 36, nil, ui.Pick{At: 32.6, Action: 0}),

			playShot("h11-trackpad-start", "input.trackpad", 0.8, nil),
			playShot("h11-trackpad-corners", "input.trackpad", 2.1, nil),
			playShot("h11-trackpad-scroll", "input.trackpad", 7.2, nil),
			playShot("h11-trackpad-swipe", "input.trackpad", 9.9, nil),
			playShot("h11-trackpad-pass", "input.trackpad", 11.5, nil),
			playShot("h11-trackpad-partial", "input.trackpad", 9.4, nil, ui.Pick{At: 8.6, Action: 0}),

			playShot("h12-force-click", "input.force-touch", 1.5, nil),
			playShot("h12-force-press", "input.force-touch", 4.5, nil),
			playShot("h12-force-pass", "input.force-touch", 6, nil),
		}
	})
}
