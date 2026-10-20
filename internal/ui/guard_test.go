package ui

import (
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	g "github.com/omaboot/boot-live/internal/grid"
)

// guardStub counts what Finish asks of the backend.
type guardStub struct {
	Backend
	submitted, saved int
}

func (s *guardStub) Submit(string, string) { s.submitted++ }
func (s *guardStub) Save(string, string)   { s.saved++ }

// Submit on a run marked in the console opens the "Changed in the console" box, and with
// DoesItOmarchy.com as the target no choice in it sends anything; a staging
// target still takes the report.
func TestGuardNeverSendsToProduction(t *testing.T) {
	for _, prod := range []bool{true, false} {
		for pick := 1; pick <= 3; pick++ {
			be := &guardStub{}
			f := &Facts{Site: "DoesItOmarchy.com", CanSubmit: true, Production: prod}
			m := &Model{st: NewState(), f: f, be: be}
			m.st.Scene, m.st.T, m.st.Net.Online = Finish, 10, true
			m.st.Con.Marks = []string{"force boot.install pass"}
			m.key(tea.KeyMsg{Type: tea.KeyEnter}) // Submit report
			if !m.st.Guard || m.st.Submitting {
				t.Fatalf("prod %v: Submit on a marked run: guard %v, form %v", prod, m.st.Guard, m.st.Submitting)
			}
			m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strconv.Itoa(pick))})
			m.key(tea.KeyMsg{Type: tea.KeyEnter})
			switch {
			case prod && be.submitted > 0:
				t.Errorf("choice %d sent a marked run to DoesItOmarchy.com", pick)
			case !prod && pick == 1 && be.submitted != 1:
				t.Errorf("staging: Send to staging sent %d reports", be.submitted)
			}
		}
	}
}

// The screensaver starts with the console open, after the same idle time, and covers it too;
// the waking key does nothing else, and the console is still open as it was.
func TestScreensaverOverConsole(t *testing.T) {
	m := &Model{st: NewState(), f: &Facts{}}
	m.st.Scene = Results
	m.st.Con.Open = true
	m.st.Con.Input = []rune("ids")
	if !m.waiting() {
		t.Fatal("Quick results with the console open isn't waiting for input")
	}
	gr := g.New(160, 50)
	if conHeight(gr, &m.st) == 0 {
		t.Fatal("the open console takes no rows")
	}
	m.st.Doze = true
	if conHeight(gr, &m.st) != 0 {
		t.Error("Boot dozes, and the console's box is still drawn")
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.st.Doze || !m.st.Con.Open || string(m.st.Con.Input) != "ids" {
		t.Errorf("after the waking key: dozing %v, console open %v, its line %q", m.st.Doze, m.st.Con.Open, string(m.st.Con.Input))
	}
}
