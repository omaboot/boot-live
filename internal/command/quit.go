package command

import "github.com/omaboot/boot-live/internal/i18n"

// quit, restart and shutdown open the screens' own quit question (Esc's) with that choice
// selected, and close the console (owner, 2026-10-15); exit is quit's other name.
func init() {
	end := func(choice string) func(Env, []string, *Out) error {
		return func(env Env, _ []string, _ *Out) error { return env.End(choice) }
	}
	Register(Cmd{Name: "quit", Help: i18n.M("ask to quit (Esc's question)"), Order: 190, Run: end("quit")})
	Register(Cmd{Name: "exit", Help: i18n.M("the same as quit"), Order: 191, Hidden: true, Run: end("quit")})
	Register(Cmd{Name: "restart", Help: i18n.M("ask to restart"), Order: 192, Run: end("restart")})
	Register(Cmd{Name: "shutdown", Help: i18n.M("ask to shut down"), Order: 193, Run: end("shutdown")})
}
