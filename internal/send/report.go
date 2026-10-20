package send

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// SourceID is boot-live's source at DoesItOmarchy (data/sources/boot-live.yaml).
const SourceID = "boot-live"

// Input is everything a report is built from.
type Input struct {
	HW       identify.Hardware
	Ident    identify.Result
	Config   string // the configuration: exactly matched or picked; "" when unknown
	Items    map[string]report.FileItem
	Extras   []report.FileExtra
	Fixes    []string            // registered fix IDs that took effect
	Ports    []report.PortAnswer // the port tests' answers for DoesItOmarchy.com's port wiring
	Omarchy  string              // the Omarchy version the image carries
	Image    string              // e.g. "omaboot-live dev 2026-10-10"
	Version  string              // boot-live's version
	Handle   string              // "Name to credit", optional
	Notice   string              // the consent notice shown before sending
	Notes    string
	TestedAt time.Time
}

// Build makes the schema v1 report. The hardware probe carries the IDs that identify the
// configuration (product name, board ID, CPU, PCI and USB IDs) and a few facts for review
// (memory, firmware version, chassis, EFI, each PCI function's class, subsystem and driver):
// never serial numbers, MAC addresses or host names, which the tool doesn't read at all.
func Build(in Input) *report.File {
	p := in.HW.Probe()
	var devs []string
	for _, d := range in.HW.PCI {
		devs = append(devs, strings.TrimSpace(fmt.Sprintf("%s %s %s %s %s", d.ShortSlot(), shortClass(d.Class), d.ID(), d.Subsys(), d.Driver)))
	}
	hw := map[string]any{
		"product_name": in.HW.ProductName,
		"board_id":     in.HW.BoardID,
		"cpu":          in.HW.CPU,
		"pci":          p.PCI,
		"usb":          p.USB,
		"pci_devices":  devs,
		"ram_mib":      in.HW.MemMiB,
		"efi_bits":     in.HW.EFIBits,
		"firmware":     in.HW.Firmware,
		"chassis_type": in.HW.Chassis,
	}
	f := &report.File{
		Schema:     report.SchemaV1,
		Config:     in.Config,
		Identifier: in.Ident.Identifier,
		Source:     report.FileSource{ID: SourceID, Version: in.Version, Profile: "quick"},
		Tester:     report.FileTester{Handle: strings.TrimSpace(in.Handle)},
		TestedAt:   in.TestedAt.UTC().Format(time.RFC3339),
		Context:    report.ContextLive,
		Omarchy:    report.FileOmarchy{Version: in.Omarchy, Image: in.Image},
		Kernel:     in.HW.Kernel,
		Notes:      in.Notes,
		Fixes:      in.Fixes,
		Ports:      in.Ports,
		Hardware:   hw,
		Items:      in.Items,
		Extras:     in.Extras,

		ConsentNotice: in.Notice,
	}
	return f
}

func shortClass(c string) string {
	if len(c) >= 4 {
		return c[:4]
	}
	return c
}

// Check runs pkg/report's Check, the server's own first pass, on the report.
func Check(f *report.File) error { return report.Check(f, time.Now()) }

var versionLike = regexp.MustCompile(`^[vV]?\d+(\.\d+){0,2}`)

// OmarchyVersion is the Omarchy version the image carries: /usr/share/omaboot/omarchy-version
// (written at image build time), else /etc/os-release's VERSION_ID when it is Omarchy's, else
// the omarchy package in the image's package database, else fallback (the release the image is
// built from).
func OmarchyVersion(r sysroot.Root, fallback string) string {
	if v := strings.TrimSpace(r.Read("/usr/share/omaboot/omarchy-version")); versionLike.MatchString(v) {
		return v
	}
	osr := map[string]string{}
	for _, l := range r.Lines("/etc/os-release") {
		if k, v, ok := strings.Cut(l, "="); ok {
			osr[k] = strings.Trim(v, `"`)
		}
	}
	if osr["ID"] == "omarchy" && versionLike.MatchString(osr["VERSION_ID"]) {
		return osr["VERSION_ID"]
	}
	for _, d := range r.Glob("/var/lib/pacman/local/omarchy-[0-9]*") {
		name := strings.TrimPrefix(path.Base(d), "omarchy-")
		if i := strings.LastIndex(name, "-"); i > 0 {
			name = name[:i] // drop the package release
		}
		if versionLike.MatchString(name) {
			return name
		}
	}
	return fallback
}

var dateLike = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Image names the image for omarchy.image: /usr/share/omaboot/image when the build wrote one,
// else from /usr/share/omaboot/build ("live 2026-10-10" → "omaboot-live dev 2026-10-10"; a
// release "live 2026.10" → "omaboot-live 2026.10").
func Image(r sysroot.Root, edition string) string {
	if s := strings.TrimSpace(r.Read("/usr/share/omaboot/image")); s != "" {
		return s
	}
	b := strings.Fields(r.Read("/usr/share/omaboot/build"))
	if len(b) == 0 {
		return "omaboot-" + edition + " dev"
	}
	name, rest := "omaboot-"+b[0], strings.Join(b[1:], " ")
	if rest == "" || dateLike.MatchString(rest) {
		return strings.TrimSpace(name + " dev " + rest)
	}
	return name + " " + rest
}

// Handle turns what was typed as the "Name to credit" into a handle the server accepts (1–39
// letters, digits, '.', '_' or '-', starting with a letter or digit): spaces become '_', other
// characters are dropped.
func Handle(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == ' ':
			r = '_'
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-':
		default:
			continue
		}
		if b.Len() == 0 && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			continue
		}
		if b.Len() < 39 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
