package command

import (
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

func init() {
	Register(Cmd{Name: "why", Args: i18n.M("<test>"), Help: i18n.M("the evidence behind a result"), Order: 20,
		Complete: testArg,
		Run: func(env Env, args []string, out *Out) error {
			if len(args) == 0 {
				return Err(i18n.T("name a test (Tab lists them)"))
			}
			r, ok := env.Result(args[0])
			if !ok {
				return Err(i18n.T("no result for %s in this run (Tab lists them)", args[0]))
			}
			head := Line{S(r.ID+"  ", Arg), S(StatusWord(r.Status), StatusColor(r.Status))}
			if r.Label != "" {
				head = append(head, T("  "+r.Label))
			}
			out.Line(head...)
			ev := r.Evidence
			if ev == "" {
				ev = r.Detail
			}
			for _, l := range Wrap(ev, env.Width()-2) {
				out.Line(T("  " + l))
			}
			field := func(k, v string) {
				if v != "" {
					out.Line(S("  "+pad(k, 8), Label), T(v))
				}
			}
			field(i18n.T("method"), r.Method)
			field(i18n.T("reason"), r.Reason)
			field(i18n.T("fix"), r.Fix)
			if r.Forced {
				out.Line(S("  "+i18n.T("set by hand in the console"), Mark))
			}
			return nil
		}})
}

// testArg completes the first argument over the run's tests.
func testArg(env Env, args []string) []string {
	if len(args) == 0 {
		return env.Tests()
	}
	return nil
}

// Wrap breaks s into lines of at most w cells at spaces (a longer word gets a line of its own).
func Wrap(s string, w int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	for _, word := range strings.Fields(s) {
		ww := width(word)
		if n > 0 && n+1+ww > w {
			out = append(out, cur.String())
			cur.Reset()
			n = 0
		}
		if n > 0 {
			cur.WriteByte(' ')
			n++
		}
		cur.WriteString(word)
		n += ww
	}
	if n > 0 {
		out = append(out, cur.String())
	}
	return out
}
