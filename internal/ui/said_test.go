package ui

import (
	"testing"
	"time"
)

// Boot's line from the console waits for a box with room, then keeps it until his own line
// there changes or its time is up.
func TestSaidLine(t *testing.T) {
	s := Said{Text: "hi", Wait: true}
	own := []line{{0, "his own"}}
	for i := 0; i < 3; i++ { // no room for his box: it waits
		s.frame()
	}
	if s.Text == "" || !s.Wait {
		t.Fatal("ended before it showed")
	}
	for i := 0; i < 3; i++ {
		s.frame()
		if got := s.over(own, 5); len(got) != 1 || got[0].text != "hi" {
			t.Fatalf("frame %d: %v", i, got)
		}
	}
	s.over(append(own, line{4, "his next"}), 5) // his own line changes: his is back next frame
	s.frame()
	if got := s.over(own, 5); s.Text != "" || got[0].text != "his own" {
		t.Errorf("still up after his own line changed: %v", got)
	}
	s = Said{Text: "hi", At: time.Now().Add(-sayFor)}
	if s.frame(); s.Text != "" {
		t.Error("still up after its time")
	}
}
