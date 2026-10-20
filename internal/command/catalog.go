package command

import (
	"fmt"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// catalog reads the catalog snapshot the run matches against: offline,
// from the snapshot in the image or the one refreshed from the site in this run.
func init() {
	Register(Cmd{Name: "catalog", Args: i18n.M("<what>"), Help: i18n.M("the catalog snapshot, offline (Tab lists what)"), Order: 45,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return catalogSubs
			}
			return nil
		},
		Run: catalog})
}

var catalogSubs = []string{"source", "mac", "search", "fixes", "ports"}

// searchMax is the most releases search lists; the rest are counted.
const searchMax = 12

func catalog(env Env, args []string, out *Out) error {
	if len(args) == 0 {
		for _, s := range []struct{ name, args, says string }{
			{"source", "", i18n.T("where it came from, its version and date")},
			{"mac", "", i18n.T("this Mac as the catalog matched it")},
			{"search", i18n.T("<text>"), i18n.T("Macs by identifier or name")},
			{"fixes", "", i18n.T("the fixes it lists for this configuration")},
			{"ports", "", i18n.T("this Mac's ports")},
		} {
			out.Line(S("catalog ", Value), S(pad(s.name, 7), Value), S(pad(s.args, 8), Arg), T(s.says))
		}
		return nil
	}
	c := env.Catalog()
	field := func(k string, v ...Seg) { out.Line(append([]Seg{S(pad(k, 10), Label)}, v...)...) }
	switch args[0] {
	case "source":
		if c.Bundled {
			field(i18n.T("source"), T(i18n.T("bundled in this image")))
		} else {
			field(i18n.T("source"), T(i18n.T("refreshed from %s in this run", c.Site)))
		}
		switch {
		case c.Site == "":
			field(i18n.T("server"), S(i18n.T("none: this image has no site to refresh from"), Label))
		case c.Fetched && c.Bundled:
			field(i18n.T("server"), S(c.Site, Arg), S("  "+i18n.T("checked in this run: nothing newer"), Label))
		case !c.Fetched:
			field(i18n.T("server"), S(c.Site, Arg), S("  "+i18n.T("not reached in this run"), Label))
		default:
			field(i18n.T("server"), S(c.Site, Arg))
		}
		field(i18n.T("version"), S(c.Version, Value))
		if c.Date != "" {
			field(i18n.T("date"), S(c.Date, Value))
		}
		field(i18n.T("holds"), S(fmt.Sprint(c.Macs), Value), T(" "+i18n.T("Macs")+", "), S(fmt.Sprint(c.Configs), Value), T(" "+i18n.T("configurations")))
	case "mac":
		m := c.Mac
		if !m.Known {
			return Err(i18n.T("this Mac isn't in the catalog: %s", m.Identifier))
		}
		field(i18n.T("model"), S(m.Identifier, Arg))
		if m.Release != "" {
			field(i18n.T("release"), S(m.Release, Value))
		}
		if m.Config == "" {
			field(i18n.T("config"), S(i18n.T("not settled: several fit"), Label))
		} else {
			field(i18n.T("config"), S(m.Config, Arg))
		}
		if m.By != "" {
			field(i18n.T("found by"), T(byWords(m.By)))
		}
		for i, id := range m.Matched {
			k := ""
			if i == 0 {
				k = i18n.T("matched")
			}
			field(k, S(id, Arg))
		}
	case "search":
		q := strings.ToLower(strings.Join(args[1:], " "))
		if q == "" {
			return Err(i18n.T("catalog search <text>"))
		}
		var hits []Release
		for _, r := range c.Releases {
			if strings.Contains(strings.ToLower(r.Identifier), q) || strings.Contains(strings.ToLower(r.Name), q) {
				hits = append(hits, r)
			}
		}
		if len(hits) == 0 {
			out.Dim(i18n.T("no Mac in the catalog matches %q", q))
			return nil
		}
		for _, r := range hits[:min(len(hits), searchMax)] {
			out.Line(S(pad(r.Identifier, 16), Arg), S(pad(r.Name, 44), Value),
				S(i18n.N("%d configuration", "%d configurations", r.Configs, r.Configs), Label))
		}
		if n := len(hits) - searchMax; n > 0 {
			out.Dim("… " + i18n.T("%d more", n))
		}
	case "fixes":
		if len(c.Fixes) == 0 {
			out.Dim(i18n.T("the catalog lists no fixes for this configuration"))
			return nil
		}
		for _, f := range c.Fixes {
			ran := S(i18n.T("not seen in this run"), Label)
			if f.Ran {
				ran = S(i18n.T("took effect in this run"), Value)
			}
			out.Line(S(pad(f.Name, 34), Value), S(pad(f.ID, 40), Arg), ran)
		}
	case "ports":
		if len(c.Ports) == 0 {
			out.Dim(i18n.T("the catalog has no port map for this Mac yet"))
			return nil
		}
		for _, p := range c.Ports {
			out.Line(S(pad(p.Side, 7), Label), S(p.Name, Value))
		}
	default:
		return Err(i18n.T("catalog %s: no such part (Tab lists them)", args[0]))
	}
	return nil
}

// byWords is how identify found the model, in words.
func byWords(by string) string {
	switch by {
	case "product_name":
		return i18n.T("its model identifier")
	case "board_id":
		return i18n.T("its board ID")
	case "devices":
		return i18n.T("its devices")
	}
	return by
}
