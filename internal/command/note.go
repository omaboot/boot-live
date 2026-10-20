package command

import (
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// maxNote keeps one note well inside the report's notes (10,000 bytes in all).
const maxNote = 500

func init() {
	Register(Cmd{Name: "note", Args: i18n.M("<text>"), Help: i18n.M("add a note to the report"), Order: 60,
		Run: func(env Env, args []string, out *Out) error {
			text := strings.Join(args, " ")
			if text == "" {
				return Err(i18n.T("write the note after it: note <text>"))
			}
			if width(text) > maxNote {
				return Err(i18n.T("at most %d characters", maxNote))
			}
			if err := env.Note(text); err != nil {
				return err
			}
			// notes are public once the report is accepted (owner, 2026-10-14)
			out.Dim(i18n.T("noted; shown on the report's page once accepted"))
			return nil
		}})
}
