//go:build omaboot_dev

package command

import (
	"fmt"

	"github.com/omaboot/boot-live/internal/i18n"
)

func init() {
	Register(Cmd{Name: "fps", Help: i18n.M("frame timing"), Dev: true, Order: 150, Run: func(env Env, _ []string, out *Out) error {
		f := env.Frames()
		dot := S("  ·  ", Label)
		out.Line(S(fmt.Sprintf("%.1f", f.RenderMS), Value), S(" "+i18n.T("ms to draw a frame"), Label), dot,
			S(fmt.Sprintf("%.0f", f.FPS), Value), S(" fps", Label), dot,
			S(fmt.Sprintf("%d", f.Rows), Value), S(" "+i18n.T("rows redrawn a frame"), Label), dot,
			S(fmt.Sprintf("%d×%d", f.W, f.H), Value))
		return nil
	}})
}
