package command

import (
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// SayMax is the most boot say takes: Boot's box holds it on the smallest console (120×40).
const SayMax = 60

func init() {
	Register(Cmd{Name: "boot", Args: i18n.M("say <text>"), Help: i18n.M("have Boot say something"), Order: 170,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"say"}
			}
			return nil
		},
		// his own box on the screen showing, not the console (owner, 2026-10-15): the command's
		// echo is all the console prints
		Run: func(env Env, args []string, _ *Out) error {
			if len(args) == 0 || args[0] != "say" {
				return Err(i18n.T("boot say <text>"))
			}
			said := strings.Join(args[1:], " ")
			if said == "" {
				said = "…"
			}
			if width(said) > SayMax {
				return Err(i18n.T("at most %d characters", SayMax))
			}
			env.Say(said)
			return nil
		}})
}
