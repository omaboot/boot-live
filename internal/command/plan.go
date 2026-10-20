package command

import "github.com/omaboot/boot-live/internal/i18n"

// plan shows the run plan (omaboot-plan.yaml on the stick, LIVE-PLAN §30) this run uses, its
// Wi-Fi password never shown; plan check reads the file on the stick again and checks it, with
// the line and the fix for each problem; plan run starts it from the screen the run is on (one
// with start: console, one ignored at Hello, or one stopped), and plan stop hands the run back.
func init() {
	Register(Cmd{Name: "plan", Args: i18n.M("[check|run|stop]"), Help: i18n.M("the run plan on the stick; check the file, run it from here, stop it"), Order: 47,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"check", "run", "stop"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			var lines []string
			var err error
			switch {
			case len(args) == 0:
				lines, err = env.Plan(false)
			case args[0] == "check":
				lines, err = env.Plan(true)
			case args[0] == "run":
				lines, err = env.PlanRun()
			case args[0] == "stop":
				lines, err = env.PlanStop()
			default:
				return Err(i18n.T("plan, plan check, plan run or plan stop"))
			}
			if err != nil {
				return err
			}
			for _, l := range lines {
				out.Line(T(l))
			}
			return nil
		}})
}
