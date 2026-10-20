package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "close", Help: i18n.M("close the console"), Order: 180, Run: func(_ Env, _ []string, out *Out) error {
		out.Close = true
		return nil
	}})
}
