//go:build omaboot_dev

package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "goto", Args: i18n.M("<screen>"), Help: i18n.M("jump to a screen seen in this run"), Dev: true, Order: 130,
		Complete: func(env Env, args []string) []string {
			if len(args) == 0 {
				return env.Screens()
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) != 1 {
				return Err(i18n.T("goto <screen> (Tab lists them)"))
			}
			if err := env.Goto(args[0]); err != nil {
				return err
			}
			out.Dim("→ " + args[0])
			return nil
		}})
}
