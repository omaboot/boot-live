package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "ids", Help: i18n.M("this Mac's hardware IDs"), Order: 40, Run: func(env Env, _ []string, out *Out) error {
		ids := env.IDs()
		if len(ids) == 0 {
			return Err(i18n.T("none read"))
		}
		for _, id := range ids {
			l := Line{S(pad(id.Key, 9), Label), S(id.ID, Arg)}
			if id.Name != "" {
				l = append(l, T("  "+id.Name))
			}
			out.Line(l...)
		}
		return nil
	}})
}
