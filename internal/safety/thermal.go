package safety

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// Sensor is one temperature input of a CPU or GPU hwmon device.
type Sensor struct {
	Chip  string // hwmon name: coretemp, k10temp, amdgpu, radeon, nouveau
	Label string
	Input string // the temp*_input path
	Crit  float64
}

// Chips whose temperatures the watchdog follows: the CPU (coretemp needs no SMC) and the GPUs.
var watched = map[string]bool{"coretemp": true, "k10temp": true, "amdgpu": true, "radeon": true, "nouveau": true, "i915": true}

// defaultCrit is the critical temperature assumed when a sensor gives none.
const defaultCrit = 100.0

// margin is how far below a sensor's critical temperature the watchdog stops the load.
const margin = 5.0

// Sensors lists the CPU and GPU temperature sensors.
func Sensors(r sysroot.Root) []Sensor {
	var out []Sensor
	for _, d := range r.Glob("/sys/class/hwmon/hwmon*") {
		chip := r.Read(d + "/name")
		if !watched[chip] {
			continue
		}
		for _, in := range r.Glob(d + "/temp*_input") {
			base := strings.TrimSuffix(path.Base(in), "_input")
			s := Sensor{Chip: chip, Label: r.Read(d + "/" + base + "_label"), Input: in, Crit: defaultCrit}
			if c, ok := r.Int(d + "/" + base + "_crit"); ok && c > 20000 {
				s.Crit = float64(c) / 1000
			} else if c, ok := r.Int(d + "/" + base + "_max"); ok && c > 20000 {
				s.Crit = float64(c) / 1000
			}
			out = append(out, s)
		}
	}
	return out
}

// Read returns a sensor's temperature in °C.
func (s Sensor) Read(r sysroot.Root) (float64, bool) {
	v, ok := r.Int(s.Input)
	if !ok || v <= -40000 || v > 150000 {
		return 0, false
	}
	return float64(v) / 1000, true
}

// Watchdog reads the CPU and GPU temperatures every second for the whole run. Near a sensor's
// critical temperature it raises Hot, and any load (the thermal check) stops at once and waits
// for the chip to cool; the stop is recorded with the check's evidence.
type Watchdog struct {
	r       sysroot.Root
	sensors []Sensor
	mu      sync.Mutex
	last    map[string]float64
	hot     bool
	stops   []string
}

// Watch starts a watchdog that runs until ctx ends.
func Watch(ctx context.Context, r sysroot.Root) *Watchdog {
	w := &Watchdog{r: r, sensors: Sensors(r), last: map[string]float64{}}
	w.sample()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.sample()
			}
		}
	}()
	return w
}

func (w *Watchdog) sample() {
	hot := false
	var why string
	vals := map[string]float64{}
	for _, s := range w.sensors {
		v, ok := s.Read(w.r)
		if !ok {
			continue
		}
		vals[s.Input] = v
		if v >= s.Crit-margin {
			hot = true
			why = strings.TrimSpace(s.Chip+" "+s.Label) + " " + fmtC(v)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = vals
	if hot && !w.hot {
		w.stops = append(w.stops, why)
	}
	w.hot = hot
}

// Hot reports whether any watched chip is near its critical temperature.
func (w *Watchdog) Hot() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hot
}

// Stops lists the moments the watchdog stopped the load, e.g. "coretemp Core 0 96 °C".
func (w *Watchdog) Stops() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.stops...)
}

// Sensors are the sensors the watchdog follows.
func (w *Watchdog) Sensors() []Sensor {
	if w == nil {
		return nil
	}
	return w.sensors
}

func fmtC(v float64) string { return fmt.Sprintf("%.0f °C", v) }
