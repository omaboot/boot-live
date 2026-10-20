//go:build omaboot_dev

package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	// it only shows where the image sends things, one line per endpoint: no switching
	Register(Cmd{Name: "target", Help: i18n.M("where reports, bug reports and the network tests go"), Dev: true, Order: 120, Run: func(env Env, _ []string, out *Out) error {
		t := env.Target()
		line := func(what, url, note string) {
			out.Line(S(what+" ", Label), S(url, Arg), S("  "+note, Label))
		}
		report := i18n.T("Diagnostic report:")
		switch {
		case t.URL == "":
			out.Line(S(report+" ", Label), S(i18n.T("none in this image: external storage only"), Label))
		case t.Production:
			line(report, t.URL, i18n.T("DoesItOmarchy.com itself: a run changed in the console goes to external storage only"))
		default:
			line(report, t.URL, i18n.T("staging: it takes a run changed in the console"))
		}
		if t.URL != "" && !t.CanSubmit {
			out.Dim(i18n.T("no report key in this image: save the report to external storage"))
		}
		bug := i18n.T("Bug report:")
		switch {
		case t.Bugs == "":
			out.Line(S(bug+" ", Label), S(i18n.T("none in this image: kept on external storage"), Label))
		case t.CanSendBug:
			line(bug, t.Bugs, i18n.T("upload key in the image"))
		default:
			line(bug, t.Bugs, i18n.T("no upload key in this image: kept on external storage"))
		}
		if t.Watch != "" {
			line(i18n.T("Network tests:"), t.Watch, i18n.T("the Wi-Fi and Ethernet tests measure against it"))
		}
		return nil
	}})
}
