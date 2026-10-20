package command

import (
	"regexp"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

func init() {
	Register(Cmd{Name: "report", Args: i18n.M("[json]"), Help: i18n.M("this run's report so far"), Order: 50,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"json"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) > 0 && args[0] == "json" {
				b, err := env.ReportJSON()
				if err != nil {
					return err
				}
				for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
					out.Line(JSONLine(l)...)
				}
				return nil
			}
			if len(args) > 0 {
				return Err(i18n.T("report, or report json"))
			}
			s := env.Summary()
			dot := S("  ·  ", Label)
			l := Line{S(i18n.N("%d passed", "%d passed", s.Pass, s.Pass), g.BGRN)}
			if s.Partial > 0 {
				l = append(l, dot, S(i18n.N("%d partial", "%d partial", s.Partial, s.Partial), g.AMB))
			}
			fc := Label
			if s.Fail > 0 {
				fc = Bad
			}
			l = append(l, dot, S(i18n.N("%d failed", "%d failed", s.Fail, s.Fail), fc),
				dot, S(i18n.N("%d needs Omarchy installed to test", "%d need Omarchy installed to test", s.Live, s.Live), g.YEL))
			if s.HandsOn > 0 {
				l = append(l, dot, T(i18n.N("%d hands-on result", "%d hands-on results", s.HandsOn, s.HandsOn)))
			}
			out.Line(l...)
			if s.Console > 0 {
				out.Dim(i18n.N("%d console command recorded in it", "%d console commands recorded in it", s.Console, s.Console))
			}
			out.Dim(s.State)
			return nil
		}})
}

var jsonTok = regexp.MustCompile(`("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?)|([^"\d-]+|.)`)

// JSONLine colours a line of indented JSON (owner, v50): keys grey, strings blue, numbers white,
// punctuation dim.
func JSONLine(l string) Line {
	var out Line
	for _, m := range jsonTok.FindAllStringSubmatch(l, -1) {
		switch {
		case m[1] != "" && m[2] != "":
			out = append(out, T(m[1]), S(m[2], Label))
		case m[1] != "":
			out = append(out, S(m[1], Arg))
		case m[3] != "":
			out = append(out, S(m[3], Value))
		default:
			out = append(out, S(m[4], Label))
		}
	}
	return out
}
