package command

import (
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// testfile: OmaBoot?'s test file, OMABOOT-TEST.BIN, made by
// boot-live itself (the same bytes as the download) on a drive the person names, so nobody has
// to download it on another computer. Never on its own: testfile write asks first, and only y
// writes. FAT and exFAT drives and the OmaBoot? drive's OMABOOT partition; never HFS+ (Linux
// writes it only with its journal off), the internal disks or the image's own partitions.
func init() {
	Register(Cmd{Name: "testfile", Args: i18n.M("[write <drive>]"), Help: i18n.M("make the test file on a drive you name"), Kind: Write, Order: 75,
		Complete: func(env Env, args []string) []string {
			switch len(args) {
			case 0:
				return []string{"write"}
			case 1:
				var ok []string
				for _, d := range env.TestDrives() {
					if d.OK {
						ok = append(ok, d.Dev)
					}
				}
				return ok
			}
			return nil
		},
		Run: testfile})
}

// TestFileName is the test file's name, the download's (the same bytes, the same name).
const TestFileName = "OMABOOT-TEST.BIN"

func testfile(env Env, args []string, out *Out) error {
	drives := env.TestDrives()
	if len(args) == 0 {
		out.Line(S(TestFileName, Arg), T(" "+i18n.T("(32 MiB, the same file as the download) can go on:")))
		for _, d := range drives {
			if d.OK {
				out.Line(S("  ■ ", g.BVIO), S(pad(d.Dev, 7), Arg), T(d.What))
			} else {
				out.Line(S("  × ", Label), S(pad(d.Dev, 7), Label), S(d.What+"  ·  "+d.Why, Label))
			}
		}
		if len(drives) == 0 {
			out.Dim(i18n.T("  no drive plugged in"))
		}
		out.Dim(i18n.T("testfile write <drive> puts it in the drive's top folder; nothing else on it changes."))
		return nil
	}
	if args[0] != "write" || len(args) != 2 {
		return Err(i18n.T("testfile lists the drives; testfile write <drive> writes it"))
	}
	dev := args[1]
	for _, d := range drives {
		if d.Dev != dev {
			continue
		}
		if !d.OK {
			return Err(i18n.T("not on %s: %s", dev, d.Why))
		}
		out.Line(T(i18n.T("write")+" "), S(TestFileName, Arg), T(" "+i18n.T("(32 MiB) to the top folder of")+" "), S(dev, Arg), T(" ("+d.What+")?"))
		out.Dim(i18n.T("nothing else on it changes. y writes it; anything else doesn't."))
		env.Console().Ask = &Ask{Name: "testfile", No: i18n.T("not written."), Then: func(env Env, out *Out) error {
			if err := env.WriteTestFile(dev); err != nil {
				return err
			}
			out.Line(S(i18n.T("writing")+" ", Label), S(TestFileName, Arg), S(" "+i18n.T("to")+" ", Label), S(dev, Arg), S("…", Label))
			return nil
		}}
		return nil
	}
	return Err(i18n.T("no drive %s; testfile lists them", dev))
}
