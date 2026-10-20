package command

import (
	"fmt"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// smc reads one SMC key raw, read-only, through applesmc's key_at_index interface (0.7.2 review:
// the iMac10,1's light reading never moved; the owner pokes ALV0 by hand). Nothing is written
// to the SMC.
func init() {
	Register(Cmd{Name: "smc", Args: i18n.M("<key>"), Help: i18n.M("an SMC key's type and raw bytes (ALV0)"), Order: 46,
		Complete: func(_ Env, args []string) []string {
			if len(args) == 0 {
				return []string{"ALV0", "ALV1", "ALSL", "ALRV"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) != 1 || len([]rune(args[0])) != 4 {
				return Err(i18n.T("name one key, 4 characters: smc ALV0"))
			}
			k, err := env.SMC(args[0])
			if err != nil {
				return err
			}
			hex := make([]string, len(k.Data))
			for i, b := range k.Data {
				hex[i] = fmt.Sprintf("%02x", b)
			}
			bytes := i18n.N("%d byte", "%d bytes", len(k.Data), len(k.Data))
			out.Line(S(pad(k.Name, 6), Arg), S(pad(strings.TrimSpace(k.Type), 6), Label), S(pad(bytes, 10), Label), S(strings.Join(hex, " "), Value))
			if len(k.Data) == 10 && strings.HasPrefix(k.Name, "ALV") { // what applesmc's light file shows for it
				out.Dim(i18n.T("applesmc's light file reads bytes 6–7: %d", (int(k.Data[6])<<8|int(k.Data[7]))>>2))
			}
			return nil
		}})
}
