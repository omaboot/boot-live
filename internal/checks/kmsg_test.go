package checks

import (
	"strings"
	"testing"
)

// The journal fills in what the ring buffer dropped, with times, and stops where kmsg starts.
func TestWithJournal(t *testing.T) {
	js := `{"MESSAGE":"Linux version 7.2","_SOURCE_MONOTONIC_TIMESTAMP":"0"}
{"MESSAGE":[105,57,49,53],"_SOURCE_MONOTONIC_TIMESTAMP":"1207000"}
{"MESSAGE":"two\nlines","__MONOTONIC_TIMESTAMP":"2000000"}
{"MESSAGE":"PM: suspend","_SOURCE_MONOTONIC_TIMESTAMP":"712366000"}
{"MESSAGE":"PM: resume","_SOURCE_MONOTONIC_TIMESTAMP":"712400000"}
`
	recs := withJournal([]byte(js), []logRec{{712366000, "PM: suspend"}, {712400000, "PM: resume"}})
	var got []string
	for _, k := range recs {
		got = append(got, k.line(true))
	}
	want := "[    0.000] Linux version 7.2|[    1.207] i915|[    2.000] two\\x0alines|[  712.366] PM: suspend|[  712.400] PM: resume"
	if g := strings.Join(got, "|"); g != want {
		t.Errorf("got  %s\nwant %s", g, want)
	}
	if !startsAtBoot(recs) {
		t.Error("the merged log should start at boot")
	}
}
