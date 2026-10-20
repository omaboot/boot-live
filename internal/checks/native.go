package checks

import (
	"context"

	"github.com/omaboot/boot-live/internal/i18n"
)

// NativeResolution is the quick pass's display.native-resolution result made again, for the
// display test (internal/handson, backlight.go): its picture check keeps this verdict when the
// tester reads the test pattern, and marks it down when they can't. It reads only sysfs, as the
// quick pass did, so it gives the same verdict.
func NativeResolution(e *Env) Result {
	r := display(context.Background(), e)[0]
	r.ID, r.Label = "display.native-resolution", i18n.M("Internal display, native resolution")
	return r
}
