package command

import "github.com/omaboot/boot-live/internal/i18n"

func init() {
	// rerun measures again: the new measurement decides, the earlier one goes into its evidence;
	// the report records the command, and the run stays sendable
	Register(Cmd{Name: "rerun", Args: i18n.M("<test>"), Help: i18n.M("measure a test again"), Kind: Redo, Order: 70,
		Complete: testArg,
		Run: func(env Env, args []string, out *Out) error {
			if len(args) == 0 {
				return Err(i18n.T("name a test (Tab lists them)"))
			}
			what, err := env.Rerun(args[0])
			if err != nil {
				return err
			}
			out.Line(T(i18n.T("rerunning")+" "), S(args[0], Arg), T("…  "), S(what, Label))
			return nil
		}})
}
