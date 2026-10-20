package pace

import (
	"math"
	"testing"
)

// An example run: the SMC trio lands as one burst. Only
// the fix gets a comment, as on the screen (partial, failed and fixed results).
func protoItems() []Item {
	return []Item{
		{ID: "boot.installer-efi64", Cue: "first: how did we get here?", Finish: 0.1, Status: Pass, Effect: true},
		{ID: "boot.install", Finish: 0.2, Status: Live},
		{ID: "display.native-resolution", Cue: "the screen. counting pixels…", Finish: 0.3, Status: Pass, Effect: true},
		{ID: "graphics.integrated", Cue: "GPU time. donuts, anyone?", Finish: 2.9, Status: Pass, Effect: true},
		{ID: "power.battery-status", Cue: "battery next.", Finish: 3.0, Status: Pass, Effect: true},
		{ID: "audio.codec", Cue: "sound chip next.", Finish: 3.4, Status: Pass, Effect: true},
		{ID: "network.wifi", Cue: "Wi-Fi next. listening…", Finish: 5.8, Status: Pass, Effect: true},
		{ID: "thermal.sensors", Cue: "now the heat.", Finish: 18.0, Status: Pass, Effect: true},
		{ID: "thermal.fans", Finish: 18.02, Status: Pass, Say: "applesmc fix on: fans, glow and light sensor all answer."},
		{ID: "input.keyboard-backlight", Cue: "keyboard glow.", Finish: 18.03, Status: Pass},
		{ID: "input.ambient-light-sensor", Cue: "light sensor.", Finish: 18.04, Status: Pass},
	}
}

func player(items []Item) *Player {
	p := New(Default, len(items), "here we go.", "quick pass done. let's see.")
	for _, it := range items {
		p.Add(it)
	}
	return p
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Each check opens with its cue, typed as its slot starts, and its slot outlasts the cue's reading.
func TestCuesOpenTheirSlots(t *testing.T) {
	p := player(protoItems())
	for _, s := range p.Slots() {
		if s.Cue == "" {
			continue
		}
		found := false
		for _, l := range p.Says() {
			if l.Kind == CueOf && l.Text == s.Cue && near(l.At, s.T0) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: cue %q not typed at its start %.2f", s.ID, s.Cue, s.T0)
		}
		if need := p.show(Line{Text: s.Cue, Kind: CueOf}); s.Shown-s.T0 < need-1e-9 {
			t.Errorf("%s: slot %.2f s, its cue needs %.2f", s.ID, s.Shown-s.T0, need)
		}
	}
}

// A line never shows before its result arrives, never closer than its hold to the line before,
// and finish times stay as measured.
func TestLinesNeverEarlyOrCrowded(t *testing.T) {
	items := protoItems()
	items = append(items,
		Item{ID: "slow", Finish: 40, Status: Fail, Effect: true},
		Item{ID: "after-slow", Finish: 40.01, Status: Pass},
		Item{ID: "own-hold", Finish: 40.02, Status: Pass, Effect: true, Hold: 5})
	p := player(items)
	prev := math.Inf(-1)
	for i, s := range p.Slots() {
		if s.Shown < Default.Start+s.Finish-1e-9 {
			t.Errorf("%s shown at %.2f before it arrived at %.2f", s.ID, s.Shown, Default.Start+s.Finish)
		}
		if h := p.hold(i, s.T0); s.Shown-prev < h-1e-9 {
			t.Errorf("%s shown %.2f s after the line before, hold is %.2f", s.ID, s.Shown-prev, h)
		}
		prev = s.Shown
	}
	for i, it := range items {
		if it.Finish != items[i].Finish {
			t.Fatal("finish times changed")
		}
	}
	slow := p.Slots()[11]
	if !near(slow.Shown, 41) {
		t.Errorf("a late result shows when it arrives: got %.2f, want 41", slow.Shown)
	}
	if hold := p.Slots()[13]; !near(hold.Shown-hold.T0, 5) {
		t.Errorf("own hold: slot is %.2f s, want 5", hold.Shown-hold.T0)
	}
}

// faceChanges lists (time, face) whenever the face changes, through the end of the playback.
func faceChanges(p *Player, until float64) (ts []float64, fs []Face) {
	var last Face = "-"
	for i := 0; float64(i)*Default.Step <= until; i++ {
		t := float64(i) * Default.Step
		f := p.At(t).Face
		if f != last {
			ts, fs, last = append(ts, t), append(fs, f), f
		}
	}
	return
}

func TestFaceChangesAtMostEveryFaceSeconds(t *testing.T) {
	p := player(protoItems())
	ts, fs := faceChanges(p, p.End()+3)
	for i := 2; i < len(ts); i++ { // the first entry is the starting face
		if ts[i]-ts[i-1] < Default.Face-1e-9 {
			t.Errorf("face %s at %.2f only %.2f s after %s", fs[i], ts[i], ts[i]-ts[i-1], fs[i-1])
		}
	}
}

// Three results whose lines land 0.8 s apart: one reaction, the worst of them, once the burst is shown.
func TestBurstGetsOneReaction(t *testing.T) {
	p := player([]Item{
		{ID: "a", Finish: 0.10, Status: Pass},
		{ID: "b", Finish: 0.11, Status: Fail},
		{ID: "c", Finish: 0.12, Status: Pass},
	})
	ts, fs := faceChanges(p, p.End()+4)
	var reactions []Face
	for _, f := range fs {
		if isReaction(f) {
			reactions = append(reactions, f)
		}
	}
	if len(reactions) != 1 || reactions[0] != Concerned {
		t.Fatalf("want one concerned reaction, got %v at %v", fs, ts)
	}
	for i, f := range fs {
		if f == Concerned && ts[i] < p.End()-1e-9 {
			t.Errorf("reacted at %.2f, before the burst's last line at %.2f", ts[i], p.End())
		}
	}
}

// A lone result gets its reaction when its line shows (no waiting for a burst).
func TestLoneResultReactsWhenShown(t *testing.T) {
	p := player(protoItems())
	disp := p.Slots()[2] // display.native-resolution, alone at 7.8
	if f := p.At(disp.Shown + 0.01).Face; f != Pleased {
		t.Errorf("face right after a lone pass is %s, want pleased", f)
	}
}

func TestBootsLinesWaitUntilRead(t *testing.T) {
	p := player(protoItems())
	says := p.Says()
	if says[0].Text != "here we go." || says[len(says)-1].Text != "quick pass done. let's see." {
		t.Fatalf("intro/outro missing: %+v", says)
	}
	for i := 1; i < len(says); i++ {
		need := p.show(says[i-1])
		if i == 1 {
			need = Default.Start
		}
		if says[i].At-says[i-1].At < need-1e-9 {
			t.Errorf("%q starts %.2f s after %q, needs %.2f", says[i].Text, says[i].At-says[i-1].At, says[i-1].Text, need)
		}
	}
	if last := says[len(says)-1]; last.At < p.End()-1e-9 {
		t.Errorf("outro before the last line shows")
	}
}

func TestProgressFollowsPlayback(t *testing.T) {
	p := player(protoItems())
	prev := -1.0
	for i := 0; float64(i)*0.05 <= p.End()+1; i++ {
		tt := float64(i) * 0.05
		f := p.At(tt)
		if f.Progress < prev-1e-9 {
			t.Fatalf("progress went back at %.2f: %.3f < %.3f", tt, f.Progress, prev)
		}
		lo, hi := float64(f.Shown)/11, float64(f.Shown+1)/11
		if f.Progress < lo-1e-9 || (f.Shown < 11 && f.Progress >= hi+1e-9) {
			t.Fatalf("progress %.3f at %.2f with %d lines shown", f.Progress, tt, f.Shown)
		}
		prev = f.Progress
	}
	if p.At(0).Progress != 0 || p.At(p.End()).Progress != 1 {
		t.Errorf("progress should run 0 → 1")
	}
}

// Results added as they arrive play back exactly like the same results known up front.
func TestLiveArrivalMatchesBatch(t *testing.T) {
	items := protoItems()
	batch := player(items)
	live := New(Default, len(items), "here we go.", "quick pass done. let's see.")
	next := 0
	for i := 0; float64(i)*Default.Step <= batch.End()+3; i++ {
		tt := float64(i) * Default.Step
		for next < len(items) && items[next].Finish <= tt {
			live.Add(items[next])
			next++
		}
		a, b := live.At(tt), batch.At(tt)
		if a.Face != b.Face || a.Shown != b.Shown || a.Say != b.Say || !near(a.Progress, b.Progress) {
			t.Fatalf("at %.2f live %+v, batch %+v", tt, a, b)
		}
	}
}

// A long check is shown running (its band effect) from its real start until its line, even
// before its result is in; effects queued ahead of it hold only Rules.Face meanwhile, and the
// lines keep their pacing (TestLinesNeverEarlyOrCrowded's rules).
func TestRunningCheckShownWhileItRuns(t *testing.T) {
	quick := []Item{
		{ID: "gpu", Begin: 2, Finish: 8, Status: Pass, Effect: true}, // after the GPU notice
		{ID: "battery", Begin: 8, Finish: 8.01, Status: Pass, Effect: true},
		{ID: "audio", Begin: 8.01, Finish: 8.02, Status: Pass, Effect: true},
	}
	heat := Item{ID: "thermal", Begin: 8.02, Finish: 28, Status: Pass, Effect: true}
	p := New(Default, 4, "", "")
	p.Started(2, "")
	for tt := 2.0; tt < 8; tt += 0.25 {
		if f := p.At(tt); !f.Running || !near(f.Local, tt-2) {
			t.Fatalf("at %.2f the GPU check isn't shown running from its start: %+v", tt, f)
		}
	}
	for _, it := range quick {
		p.Add(it)
	}
	p.Started(heat.Begin, "")
	s := p.Slots()
	if !near(s[0].T0, 2) || !near(s[0].Shown, 9) {
		t.Errorf("gpu slot %.2f–%.2f, want 2.00–9.00", s[0].T0, s[0].Shown)
	}
	for _, x := range s[1:] {
		if !near(x.Shown-x.T0, Default.Face) {
			t.Errorf("%s holds %.2f s while thermal runs, want %.2f", x.ID, x.Shown-x.T0, Default.Face)
		}
	}
	from := p.End()
	if f := p.At(from - 0.01); f.Running {
		t.Errorf("thermal shown running before the queued effects are done")
	}
	if f := p.At(from + 5); !f.Running || !near(f.Local, 5) {
		t.Errorf("thermal not shown running at %.2f: %+v", from+5, p.At(from+5))
	}
	p.Add(heat)
	last := p.Slots()[3]
	if !near(last.T0, from) || !near(last.Shown, Default.Start+heat.Finish) {
		t.Errorf("thermal slot %.2f–%.2f, want %.2f–%.2f", last.T0, last.Shown, from, Default.Start+heat.Finish)
	}
	if f := p.At(from + 5); f.Current != 3 || !near(f.Local, 5) {
		t.Errorf("the effect jumped when the result came in: %+v", f)
	}
}
