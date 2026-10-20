// Package identify reads what this Mac says about itself (DMI, the Apple board ID, the CPU, PCI
// and USB IDs, memory) and matches it against the catalog snapshot with doesitomarchy's
// pkg/match, the same code the site's Identify page and report intake run.
//
// Nothing personal is read: no serial numbers (DMI's product_serial, board_serial,
// product_uuid), no MAC addresses, no host names.
package identify

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/match"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// PCIDev is one PCI function.
type PCIDev struct {
	Slot      string // 0000:00:02.0
	Class     string // six hex digits, e.g. 030000
	Vendor    string // four hex digits
	Device    string
	SubVendor string
	SubDevice string
	Driver    string // the bound driver, or ""
	BootVGA   bool
}

// ID is "vvvv:dddd".
func (d PCIDev) ID() string { return d.Vendor + ":" + d.Device }

// Subsys is the subsystem "vvvv:dddd", or "".
func (d PCIDev) Subsys() string {
	if d.SubVendor == "" {
		return ""
	}
	return d.SubVendor + ":" + d.SubDevice
}

// ShortSlot drops the PCI domain: "00:02.0".
func (d PCIDev) ShortSlot() string { return strings.TrimPrefix(d.Slot, "0000:") }

// IsGPU reports a display controller (class 03xxxx).
func (d PCIDev) IsGPU() bool { return strings.HasPrefix(d.Class, "03") }

// USBDev is one USB device (not an interface).
type USBDev struct {
	Path    string // sysfs name, e.g. 2-1.2
	Vendor  string
	Product string
	Storage bool // a mass-storage device (a stick or a disk): not part of the Mac
	Hub     bool
}

// ID is "vvvv:pppp".
func (d USBDev) ID() string { return d.Vendor + ":" + d.Product }

// Hardware is what the Mac reports about itself.
type Hardware struct {
	ProductName string // the model identifier, e.g. MacBookAir5,2
	BoardID     string // Apple's board ID, e.g. Mac-2E6FAB96566FE58C (DMI board_name)
	BoardVendor string
	SysVendor   string
	Family      string // DMI product_family, e.g. "MacBook Air"
	Firmware    string // DMI bios_version, e.g. 429.0.0.0.0
	Chassis     int    // SMBIOS chassis type (DMI chassis_type)
	CPU         string // /proc/cpuinfo's model name, spaces collapsed
	Threads     int
	MemMiB      int
	EFIBits     int // 64 or 32; 0 when booted without EFI (BIOS/CSM)
	Kernel      string
	Cmdline     string
	PCI         []PCIDev
	USB         []USBDev
}

var spaces = regexp.MustCompile(`\s+`)

// Read reads the hardware under root.
func Read(r sysroot.Root) Hardware {
	d := "/sys/class/dmi/id/"
	h := Hardware{
		ProductName: r.Read(d + "product_name"),
		BoardID:     r.Read(d + "board_name"),
		BoardVendor: r.Read(d + "board_vendor"),
		SysVendor:   r.Read(d + "sys_vendor"),
		Family:      r.Read(d + "product_family"),
		Firmware:    r.Read(d + "bios_version"),
		Kernel:      r.Read("/proc/sys/kernel/osrelease"),
		Cmdline:     r.Read("/proc/cmdline"),
	}
	h.Chassis, _ = r.Int(d + "chassis_type")
	for _, l := range r.Lines("/proc/cpuinfo") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "model name":
			if h.CPU == "" {
				h.CPU = strings.TrimSpace(spaces.ReplaceAllString(v, " "))
			}
		case "processor":
			h.Threads++
		}
	}
	for _, l := range r.Lines("/proc/meminfo") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.Atoi(f[1])
			h.MemMiB = kb / 1024
		}
	}
	switch r.Read("/sys/firmware/efi/fw_platform_size") {
	case "64":
		h.EFIBits = 64
	case "32":
		h.EFIBits = 32
	default:
		if r.IsDir("/sys/firmware/efi") {
			h.EFIBits = 64
		}
	}
	for _, p := range r.Glob("/sys/bus/pci/devices/*") {
		h.PCI = append(h.PCI, readPCI(r, p))
	}
	for _, p := range r.Glob("/sys/bus/usb/devices/*") {
		name := p[strings.LastIndex(p, "/")+1:]
		if strings.Contains(name, ":") { // an interface, not a device
			continue
		}
		v, pr := r.Hex(p+"/idVendor"), r.Hex(p+"/idProduct")
		if v == "" || pr == "" {
			continue
		}
		u := USBDev{Path: name, Vendor: v, Product: pr, Hub: r.Read(p+"/bDeviceClass") == "09"}
		for _, ic := range r.Glob(p + "/" + name + ":*/bInterfaceClass") {
			if r.Read(ic) == "08" {
				u.Storage = true
			}
		}
		h.USB = append(h.USB, u)
	}
	return h
}

// readPCI reads one PCI function from its sysfs directory: the uevent has the IDs and the
// driver; the class and boot_vga files the rest.
func readPCI(r sysroot.Root, p string) PCIDev {
	ue := r.Uevent(p + "/uevent")
	d := PCIDev{Slot: p[strings.LastIndex(p, "/")+1:], Driver: ue["DRIVER"]}
	if v, dv, ok := strings.Cut(strings.ToLower(ue["PCI_ID"]), ":"); ok {
		d.Vendor, d.Device = v, dv
	}
	if v, dv, ok := strings.Cut(strings.ToLower(ue["PCI_SUBSYS_ID"]), ":"); ok {
		d.SubVendor, d.SubDevice = v, dv
	}
	if d.Vendor == "" {
		d.Vendor, d.Device = r.Hex(p+"/vendor"), r.Hex(p+"/device")
	}
	if d.SubVendor == "" {
		d.SubVendor, d.SubDevice = r.Hex(p+"/subsystem_vendor"), r.Hex(p+"/subsystem_device")
	}
	if c := strings.ToLower(strings.TrimPrefix(r.Read(p+"/class"), "0x")); c != "" {
		d.Class = c
	} else if c := strings.ToLower(ue["PCI_CLASS"]); c != "" {
		d.Class = fmt.Sprintf("%06s", c)
	}
	d.BootVGA = r.Read(p+"/boot_vga") == "1"
	return d
}

// Probe is the match.Probe for this hardware: the model identifier, board ID, CPU, and the PCI
// and USB IDs of the Mac's own devices (sticks and disks plugged into USB left out).
func (h Hardware) Probe() match.Probe {
	p := match.Probe{ProductName: h.ProductName, BoardID: h.BoardID, CPU: h.CPU}
	for _, d := range h.PCI {
		p.PCI = append(p.PCI, d.ID())
	}
	for _, u := range h.USB {
		if !u.Storage {
			p.USB = append(p.USB, u.ID())
		}
	}
	p.PCI, p.USB = uniq(p.PCI), uniq(p.USB)
	return p
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// GPUs are the display controllers, the boot GPU first.
func (h Hardware) GPUs() []PCIDev {
	var out []PCIDev
	for _, d := range h.PCI {
		if d.IsGPU() {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].BootVGA && !out[j].BootVGA })
	return out
}

// PCIByClass returns the functions whose class starts with prefix (e.g. "0280" Wi-Fi).
func (h Hardware) PCIByClass(prefix string) []PCIDev {
	var out []PCIDev
	for _, d := range h.PCI {
		if strings.HasPrefix(d.Class, prefix) {
			out = append(out, d)
		}
	}
	return out
}

// PCIBySlot finds a function by its slot name.
func (h Hardware) PCIBySlot(slot string) (PCIDev, bool) {
	for _, d := range h.PCI {
		if d.Slot == slot {
			return d, true
		}
	}
	return PCIDev{}, false
}

// Integrated reports whether a GPU is an integrated one: Intel graphics, and NVIDIA's chipset
// GPUs (GeForce 9400M/9600M-class MCP79 and 320M MCP89, devices 0x0860–0x087f and
// 0x08a0–0x08a5), which share the system's memory. Everything else is discrete.
func Integrated(d PCIDev) bool {
	switch d.Vendor {
	case "8086":
		return true
	case "10de":
		n, err := strconv.ParseUint(d.Device, 16, 32)
		return err == nil && (n >= 0x0860 && n <= 0x087f || n >= 0x08a0 && n <= 0x08a5)
	}
	return false
}

// Model splits an identifier into its family and major number: "MacBookPro11,4" → "MacBookPro", 11.
func Model(identifier string) (family string, major int) {
	i := strings.IndexFunc(identifier, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return identifier, 0
	}
	n := identifier[i:]
	if j := strings.IndexByte(n, ','); j >= 0 {
		n = n[:j]
	}
	major, _ = strconv.Atoi(n)
	return identifier[:i], major
}

// LaptopFamily says whether an identifier's family is a laptop (MacBook, MacBookAir,
// MacBookPro) or a desktop (iMac, iMacPro, Macmini, MacPro, Xserve); known is false for any
// other family.
func LaptopFamily(identifier string) (laptop, known bool) {
	switch f, _ := Model(identifier); f {
	case "MacBook", "MacBookAir", "MacBookPro":
		return true, true
	case "iMac", "iMacPro", "Macmini", "MacPro", "Xserve":
		return false, true
	}
	return false, false
}

// Laptop reports whether the Mac is a laptop: by its identifier's family when that's a known
// Mac family, else by its chassis type (portable, laptop, notebook, sub-notebook). The family
// decides because Apple's chassis types aren't reliable: an iMac18,3 reports 9 (laptop).
func (h Hardware) Laptop() bool {
	if l, known := LaptopFamily(h.ProductName); known {
		return l
	}
	switch h.Chassis {
	case 8, 9, 10, 14:
		return true
	}
	return false
}

// ShortCPU is the CPU's model as the catalog names it: "Intel(R) Core(TM) i5-3427U CPU @
// 1.80GHz" → "Core i5-3427U"; "Intel(R) Core(TM)2 Duo CPU E7600 @ 3.06GHz" → "Core 2 Duo E7600".
func ShortCPU(name string) string {
	s := strings.NewReplacer("(R)", "", "(TM)", "", "(tm)", "", "Intel ", "", " CPU", "", " Processor", "").Replace(name)
	s = strings.TrimPrefix(strings.TrimSpace(s), "Intel")
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	s = strings.Replace(s, "Core2", "Core 2", 1)
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}
