//go:build omaboot_dev

package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	// it only shows where Submit sends: no switching
	Register(Cmd{Name: "target", Help: i18n.M("where Submit sends"), Dev: true, Order: 120, Run: func(env Env, _ []string, out *Out) error {
		t := env.Target()
		if t.URL == "" {
			out.Dim(i18n.T("no target in this image: external storage only"))
			return nil
		}
		note := i18n.T("staging: it takes a run changed in the console")
		if t.Production {
			note = i18n.T("DoesItOmarchy.com itself: a run changed in the console goes to external storage only")
		}
		out.Line(S(i18n.T("sending to")+" ", Label), S(t.URL, Arg), S("  "+note, Label))
		if !t.CanSubmit {
			out.Dim(i18n.T("no report key in this image: save the report to external storage"))
		}
		return nil
	}})
}
