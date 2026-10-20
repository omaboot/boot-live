package main

import "github.com/omaboot/boot-live/internal/ui"

// The challenges that back the SMC and screen tests (0.5.0, no false passes): the light sensor's
// cover, the keyboard light's blink count and the display's test pattern, played on the demo's
// scripted Mac. The code typed after a nap is in handson.go's h7 shots.
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			playShot("h2b-native-round1", "display.native-resolution", 1.2, nil),
			playShot("h2b-native-round2", "display.native-resolution", 3.4, nil, ui.Pick{At: 1.5, Action: 3}),
			playShot("h2b-native-pass", "display.native-resolution", 4.5, nil, ui.Pick{At: 1.5, Action: 3}, ui.Pick{At: 3, Action: 5}),
			playShot("h2b-native-wrong", "display.native-resolution", 4.5, nil, ui.Pick{At: 1.5, Action: 3}, ui.Pick{At: 3, Action: 0}),
			playShot("h2b-native-bad", "display.native-resolution", 2.5, nil, ui.Pick{At: 1.5, Action: 6}),
			playShot("h3-kbdlight-blinking", "input.keyboard-backlight", 4.4, nil),
			playShot("h3-kbdlight-hint", "input.keyboard-backlight", 30, nil),
			playShot("h3-kbdlight-pass", "input.keyboard-backlight", 10.4, nil, ui.Pick{At: 9.6, Action: 2}),
			playShot("h3-kbdlight-recount", "input.keyboard-backlight", 10.4, nil, ui.Pick{At: 9.6, Action: 1}),
			playShot("h3k-keys-start", "input.keyboard-backlight.keys", 1, nil),
			playShot("h3k-keys-up", "input.keyboard-backlight.keys", 3.3, nil),
			playShot("h3k-keys-down", "input.keyboard-backlight.keys", 6.1, nil),
			playShot("h3k-keys-ask", "input.keyboard-backlight.keys", 8.3, nil),
			playShot("h3k-keys-pass", "input.keyboard-backlight.keys", 10, nil, ui.Pick{At: 8.5, Action: 0}),

			playShot("h3b-lightsensor-room", "input.ambient-light-sensor", 0.8, nil),
			playShot("h3b-lightsensor-cover", "input.ambient-light-sensor", 2.5, nil),
			playShot("h3b-lightsensor-hold", "input.ambient-light-sensor", 4.5, nil),
			playShot("h3b-lightsensor-uncover", "input.ambient-light-sensor", 7, nil),
			playShot("h3b-lightsensor-pass", "input.ambient-light-sensor", 8.5, nil),
		}
	})
}
