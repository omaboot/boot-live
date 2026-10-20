package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "sounds", Help: i18n.M("pick Boot's sound for each moment"), Order: 165, Run: func(env Env, _ []string, out *Out) error {
		if err := env.Sounds(); err != nil {
			return err
		}
		out.Close = true
		return nil
	}})
}
