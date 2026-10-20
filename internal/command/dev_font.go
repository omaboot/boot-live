//go:build omaboot_dev

package command

import (
	"strconv"

	"github.com/omaboot/boot-live/internal/i18n"
)

func init() {
	// omaboot-v's size classes only; other fonts wait for PF-11
	Register(Cmd{Name: "font", Args: i18n.M("16|24|32"), Help: i18n.M("load the console font at another size"), Dev: true, Order: 140,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"16", "24", "32"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) != 1 || !oneOf(args[0], "16", "24", "32") {
				return Err(i18n.T("font 16, 24 or 32"))
			}
			px, _ := strconv.Atoi(args[0])
			if err := env.Font(px); err != nil {
				return err
			}
			out.Dim(i18n.T("font omaboot-v %d", px))
			return nil
		}})
}
