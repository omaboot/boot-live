//go:build omaboot_dev

package command

import "github.com/omaboot/boot-live/internal/i18n"

// The dev-only commands are in files built with the omaboot_dev tag (dev builds: make build with
// a dev VERSION), so a release binary doesn't contain them.

func init() {
	Register(Cmd{Name: "force", Args: i18n.M("<test> <result>"), Help: i18n.M("set a result by hand"), Kind: Change, Dev: true, Order: 90,
		Complete: func(env Env, args []string) []string {
			switch len(args) {
			case 0:
				return env.Tests()
			case 1:
				return []string{Pass, Partial, Fail, Skip}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) != 2 || !oneOf(args[1], Pass, Partial, Fail, Skip) {
				return Err(i18n.T("force <test> pass|partial|fail|skip"))
			}
			old, err := env.Force(args[0], args[1])
			if err != nil {
				return err
			}
			out.Line(S(args[0], Arg), S(": ", Label), S(StatusWord(old), StatusColor(old)), S(" → ", Label),
				S(StatusWord(args[1]), StatusColor(args[1])), S("  ("+i18n.T("forced")+")", Mark))
			return nil
		}})
}

func oneOf(s string, of ...string) bool {
	for _, o := range of {
		if s == o {
			return true
		}
	}
	return false
}
