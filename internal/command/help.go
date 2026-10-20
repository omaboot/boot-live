package command

import (
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

func init() {
	Register(Cmd{Name: "?", Help: i18n.M("list the commands"), Order: 0, Run: help})
	Register(Cmd{Name: "help", Help: i18n.M("the same as ?"), Order: 1, Hidden: true, Run: help})
}

// help lists the commands: name, arguments, what it does, "dev", and its kind as a tag in its
// own column. No legend under the list: the tags say it themselves.
func help(_ Env, _ []string, out *Out) error {
	out.Unlogged = true
	for _, c := range All() {
		if c.Hidden || c.Unlisted {
			continue
		}
		args := i18n.S(c.Args)
		d := ""
		if c.Dev {
			d = "dev"
		}
		out.Line(S(c.Name+" ", Value), S(pad(args, 25-width(c.Name)), Arg), T(pad(i18n.S(c.Help), 48)), S(pad(d, 5), Label), Tag(c.Kind))
	}
	return nil
}

func width(s string) int { return len([]rune(s)) }

// pad fills s with spaces to n cells (at least one space after it).
func pad(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s + " "
}
