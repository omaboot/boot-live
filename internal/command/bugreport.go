package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	Register(Cmd{Name: "bugreport", Args: i18n.M("[send]"), Help: i18n.M("logs, report and screen to external storage; send: to OmaBoot? to look into"), Order: 80,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"send"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) > 0 {
				if args[0] != "send" {
					return Err(i18n.T("bugreport, or bugreport send"))
				}
				if err := env.SendBug(); err != nil {
					return err
				}
				out.Dim(i18n.T("sending, scrubbed, to OmaBoot?…"))
				return nil
			}
			if err := env.Bugreport(); err != nil {
				return err
			}
			out.Dim(i18n.T("saving the full log, the kernel log, the report and this screen to external storage…"))
			return nil
		}})
}
