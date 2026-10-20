package checks

import (
	"fmt"
	"math"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// Again is what a result measured again in the console (rerun) carries in its evidence: the new
// measurement decides, the earlier one goes into its evidence ("measured again in the console;
// first run: partial, 0:42"). took is how long the first run took, in seconds.
func Again(first Result, took float64) string {
	s := first.Status
	if s == NotTested && first.Reason != "" {
		s += " (" + first.Reason + ")"
	}
	t := int(math.Round(max(0, took)))
	note := fmt.Sprintf("measured again in the console; first run: %s, %d:%02d", s, t/60, t%60)
	if ev := strings.TrimSpace(first.Evidence); ev != "" {
		note += ": " + cut(ev, 400)
	}
	return note
}

// AgainEvidence is a new measurement's evidence with the first run's note after it, kept inside
// the report's limit for one item's evidence.
func AgainEvidence(evidence, note string) string {
	e := strings.TrimSpace(evidence)
	if e != "" {
		e += "; "
	}
	e += note
	if len(e) > report.MaxEvidence {
		e = cutBytes(e, report.MaxEvidence)
	}
	return e
}

// cutBytes cuts s to at most n bytes on a character boundary.
func cutBytes(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}
