package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "clear", Help: i18n.M("clear the console"), Order: 160, Run: func(_ Env, _ []string, out *Out) error {
		out.Clear = true // the full log keeps it all
		return nil
	}})
}
