package handson

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// What the port tests read about USB from sysfs: the devices on the buses, the hub port each
// hangs off, its USB 2 / USB 3 peer, the controller behind a bus, the block devices on USB, and
// the stick OmaBoot? booted from. Reading only: the port tests never write to a device.

// ── links ─────────────────────────────────────────────────────────────────────

// portLinker is an IO that can read a sysfs symlink's target (a port's peer, a USB bus's PCI
// controller, a block device a mount names). System and Fake both are.
type portLinker interface {
	PortLink(p string) string
}

// PortLink is a symlink's target as written ("../../../usb2/2-0:1.0/usb2-port2"), or "". On a
// fixture tree it reads the tree's own links.
func (s *System) PortLink(p string) string { return s.Root.Link(p) }

// fakeLink is the file a Fake keeps a scripted symlink in: "link:" and the link's path.
const fakeLink = "link:"

// PortLink is a scripted link (FakeLink), or "".
func (f *Fake) PortLink(p string) string { return f.Files[fakeLink+p] }

// FakeLink scripts a symlink on the Fake: p points at target.
func FakeLink(f *Fake, p, target string) { f.Set(fakeLink+p, target) }

func link(io IO, p string) string {
	if l, ok := io.(portLinker); ok {
		return l.PortLink(p)
	}
	return ""
}

// ── devices ───────────────────────────────────────────────────────────────────

const usbDevices = "/sys/bus/usb/devices/"

// usbDev is one USB device (not a root hub, not an interface) as read now.
type usbDev struct {
	Path        string // 1-2, 2-1.3: bus, then the port at each hub
	ID          string // idVendor:idProduct
	Name        string // manufacturer and product, else the ID
	Speed       string // Mb/s as the kernel writes it: 1.5, 12, 480, 5000, 10000, 20000
	Hub         bool
	Storage     bool   // has a mass-storage interface
	HID         bool   // has a HID interface (a mouse, a keyboard)
	ConnectType string // its hub port's connect_type: hotplug, hard-wired, not used, unknown, or ""
	Version     string // the USB version it says it speaks (bcdUSB): "2.00", "3.10"
	SuperSpeed  bool   // its BOS descriptor lists SuperSpeed: USB 3, whatever link it got
	Serial      string // its serial number, "" for none
}

// usb3Capable reports whether the device speaks USB 3: bcdUSB 3.00 or later, or SuperSpeed in
// its BOS descriptor. On a USB 2 link a USB 3 device says bcdUSB 2.10 (the Air's 0.7.12 run: the
// OmaBoot? drive at 480 Mb/s in a USB 3 port, and no finding), so the version alone misses the
// one case the USB 2 speed finding is for.
func (d usbDev) usb3Capable() bool {
	v, _ := strconv.ParseFloat(strings.TrimSpace(d.Version), 64)
	return v >= 3 || d.SuperSpeed
}

// bosSuperSpeed reports whether a BOS descriptor (sysfs bos_descriptors) has a SuperSpeed
// (0x03) or SuperSpeedPlus (0x0a) device capability.
func bosSuperSpeed(b []byte) bool {
	if len(b) < 5 || b[1] != 0x0f {
		return false
	}
	for i := int(b[0]); i+2 < len(b); i += int(b[i]) {
		if b[i] < 3 {
			return false
		}
		if b[i+1] == 0x10 && (b[i+2] == 0x03 || b[i+2] == 0x0a) {
			return true
		}
	}
	return false
}

// isUSBDevicePath: "1-2" or "2-1.3", not "usb1" or "1-2:1.0".
func isUSBDevicePath(p string) bool {
	bus, ports, ok := strings.Cut(p, "-")
	if !ok || bus == "" || ports == "" || strings.Contains(p, ":") {
		return false
	}
	for _, s := range append([]string{bus}, strings.Split(ports, ".")...) {
		if _, err := strconv.Atoi(s); err != nil {
			return false
		}
	}
	return true
}

// readUSB lists the USB devices now, by path.
func readUSB(io IO) map[string]usbDev {
	out := map[string]usbDev{}
	for _, d := range io.Glob(usbDevices + "*") {
		p := path.Base(d)
		if !isUSBDevicePath(p) {
			continue
		}
		u := usbDev{Path: p, ID: io.Read(d+"/idVendor") + ":" + io.Read(d+"/idProduct"), Speed: io.Read(d + "/speed"),
			Hub: io.Read(d+"/bDeviceClass") == "09", ConnectType: io.Read(usbPortDir(p) + "/connect_type"),
			Version: io.Read(d + "/version"), SuperSpeed: bosSuperSpeed(io.Bytes(d + "/bos_descriptors")), Serial: io.Read(d + "/serial")}
		if u.ID == ":" { // half-made: the kernel is still adding it
			continue
		}
		u.Name = strings.TrimSpace(io.Read(d+"/manufacturer") + " " + io.Read(d+"/product"))
		if u.Name == "" {
			u.Name = u.ID
		}
		for _, i := range io.Glob(d + "/" + p + ":*") {
			switch io.Read(i + "/bInterfaceClass") {
			case "08":
				u.Storage = true
			case "03":
				u.HID = true
			}
		}
		out[p] = u
	}
	return out
}

// usbParent is the hub a device hangs off: "1-1.3" → "1-1"; a root port ("1-2") → "usb1".
func usbParent(dev string) string {
	if i := strings.LastIndexByte(dev, '.'); i >= 0 {
		return dev[:i]
	}
	bus, _, _ := strings.Cut(dev, "-")
	return "usb" + bus
}

// usbPortDir is the hub port a device hangs off: 1-2 → …/usb1/1-0:1.0/usb1-port2, 1-1.3 →
// …/1-1/1-1:1.0/1-1-port3.
func usbPortDir(dev string) string {
	parent := usbParent(dev)
	n := dev[strings.LastIndexAny(dev, "-.")+1:]
	intf := parent + ":1.0"
	if strings.HasPrefix(parent, "usb") {
		intf = strings.TrimPrefix(parent, "usb") + "-0:1.0"
	}
	return usbDevices + parent + "/" + intf + "/" + parent + "-port" + n
}

// usbPortDev is the device path at a hub port: usb2-port2 → 2-2, 2-1-port3 → 2-1.3.
func usbPortDev(port string) string {
	i := strings.LastIndex(port, "-port")
	if i < 0 {
		return ""
	}
	hub, n := port[:i], port[i+len("-port"):]
	if bus, ok := strings.CutPrefix(hub, "usb"); ok {
		return bus + "-" + n
	}
	return hub + "." + n
}

// usbPeer is the device path of the same physical port on the controller's other bus: a USB 3
// port is two hub ports, one on the USB 2 bus (1-2) and one on the SuperSpeed bus (2-2), and
// the kernel links them as each other's "peer" (from ACPI or the hub's descriptors). A USB 2
// device enumerates on one side, a USB 3 device on the other, so the peer is what tells the
// test it's the same hole in the case. "" when the port has no peer (USB 2 only, or no link).
func usbPeer(io IO, dev string) string {
	t := link(io, usbPortDir(dev)+"/peer")
	if t == "" {
		return ""
	}
	return usbPortDev(path.Base(t))
}

// pciOf is the last PCI function in a sysfs link's target (the controller under it), or "".
func pciOf(target string) string {
	out := ""
	for _, part := range strings.Split(target, "/") {
		if pciAddr.MatchString(part) {
			out = part
		}
	}
	return out
}

// usbStable is a USB device's port as DoesItOmarchy.com's wiring keeps it, the same on every
// boot, unlike bus numbers (a Thunderbolt 3 controller's bus only appears when it wakes): its
// root hub's kind (usb1 at 12 Mb/s or less, usb2 at 480, usb3 faster: an xHCI controller has a
// USB 2 and a USB 3 root hub on one PCI address), the controller's PCI address and the port at
// each hub, "usb2:0000:00:14.0/1.2". "" when sysfs doesn't say.
func usbStable(io IO, dev string) string {
	bus, ports, ok := strings.Cut(dev, "-")
	if !ok {
		return ""
	}
	root := usbDevices + "usb" + bus
	pci := pciOf(link(io, root))
	sp, err := strconv.ParseFloat(io.Read(root+"/speed"), 64)
	if pci == "" || err != nil {
		return ""
	}
	kind := "usb3"
	switch {
	case sp <= 12:
		kind = "usb1"
	case sp <= 480:
		kind = "usb2"
	}
	return kind + ":" + pci + "/" + ports
}

// usbStables are a port key's paths ("3-1 / 4-1") as usbStable gives them.
func usbStables(io IO, key string) []string {
	var out []string
	for _, d := range strings.Fields(key) {
		if s := usbStable(io, d); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// usbPortKey names a physical port: its USB 2 side and its USB 3 side ("1-2 / 2-2"), lower bus
// first, or the one path when it has no peer.
func usbPortKey(io IO, dev string) string {
	peer := usbPeer(io, dev)
	switch {
	case peer == "" || peer == dev:
		return dev
	case lessPath(peer, dev):
		return peer + " / " + dev
	}
	return dev + " / " + peer
}

func lessPath(a, b string) bool {
	ab, _, _ := strings.Cut(a, "-")
	bb, _, _ := strings.Cut(b, "-")
	x, _ := strconv.Atoi(ab)
	y, _ := strconv.Atoi(bb)
	if x != y {
		return x < y
	}
	return a < b
}

// usbPortOf is where a device that just showed up was plugged in: the highest hub port on its
// path that had nothing on it when the test began. A stick in a Mac port is that port; a device
// behind a hub the person plugged in counts as the hub's port, once. Devices that were there
// from the start (the internal keyboard, trackpad, camera and Bluetooth behind the Mac's own
// hubs) are never anyone's port.
func usbPortOf(dev string, start map[string]usbDev) string {
	parts := strings.Split(dev, ".")
	for i := 1; i <= len(parts); i++ {
		p := strings.Join(parts[:i], ".")
		if _, there := start[p]; !there {
			return p
		}
	}
	return dev
}

// usbInternal reports whether a device showing up at port p is one of the Mac's own: its hub
// port is hard-wired (ACPI says nothing can be plugged there), or the same device sat there
// when the test began and the port isn't marked as a hotplug port (an internal device that
// re-enumerated, as a Bluetooth module or camera can). A device with the tethering phone's ID
// or the OmaBoot? stick's (never, "" for none) is never one: a phone or the stick (copytoram)
// unplugged and plugged back into the same port, on a Mac whose ports say "unknown", would look
// like one.
func usbInternal(p string, d usbDev, start map[string]usbDev, never ...string) bool {
	for _, id := range never {
		if id != "" && d.ID == id {
			return false
		}
	}
	if d.ConnectType == "hard-wired" {
		return true
	}
	s, was := start[p]
	return was && s.ID == d.ID && d.ConnectType != "hotplug"
}

// ── controllers ───────────────────────────────────────────────────────────────

// tbXHCI are the USB controllers inside Intel's Thunderbolt 3 chips (Alpine Ridge, Titan
// Ridge; linux drivers/usb/host/xhci-pci.c): on a Mac, a USB bus on one of them is behind a
// USB-C / Thunderbolt 3 port. Every other Mac bus is the chipset's (or an old NEC/Fresco card's)
// and on Macs with both kinds of port, those are the USB-A ports.
var tbXHCI = map[string]bool{"0x15b5": true, "0x15b6": true, "0x15c1": true, "0x15d4": true, "0x15db": true,
	"0x15e9": true, "0x15ec": true, "0x15f0": true}

// tbControllers are the Thunderbolt 3 chips' USB controllers on this Mac, by PCI address.
func tbControllers(io IO) []string {
	var out []string
	for _, d := range io.Glob("/sys/bus/pci/devices/*") {
		if tbXHCI[strings.ToLower(io.Read(d+"/device"))] && io.Read(d+"/vendor") == "0x8086" {
			out = append(out, path.Base(d))
		}
	}
	return out
}

// usbBusController is the PCI address of a bus's controller ("0000:3a:00.0"), from the root
// hub's link (…/0000:00:1c.4/0000:06:00.0/…/0000:3a:00.0/usb3), or "".
func usbBusController(io IO, dev string) string {
	bus, _, _ := strings.Cut(dev, "-")
	t := link(io, usbDevices+"usb"+bus)
	return path.Base(path.Dir(t))
}

// onThunderbolt3 reports whether a device's bus is on a Thunderbolt 3 chip's USB controller.
func onThunderbolt3(io IO, dev string) bool {
	pci := usbBusController(io, dev)
	return pci != "" && pci != "." && tbXHCI[strings.ToLower(io.Read("/sys/bus/pci/devices/"+pci+"/device"))] &&
		io.Read("/sys/bus/pci/devices/"+pci+"/vendor") == "0x8086"
}

// ── block devices ─────────────────────────────────────────────────────────────

// usbDisks maps each block device on USB to the USB device it's on: sdb → 1-1, from
// …/devices/1-1:1.0/host6/target6:0:0/6:0:0:0/block/sdb.
func usbDisks(io IO) map[string]string {
	out := map[string]string{}
	for _, b := range io.Glob(usbDevices + "*:*/host*/target*/*/block/*") {
		rel := strings.TrimPrefix(b, usbDevices)
		intf, _, _ := strings.Cut(rel, "/")
		dev, _, _ := strings.Cut(intf, ":")
		out[path.Base(b)] = dev
	}
	return out
}

// bootDisk is the whole disk OmaBoot? booted from (sdb): from the mount of archiso's boot
// medium, or, with copytoram (archiso unmounts the medium once the image is in memory), the disk
// with the image's UUID (archisosearchuuid, as send.BootDisk finds it); "" when neither says.
func bootDisk(io IO) string {
	for _, l := range strings.Split(io.Read("/proc/mounts"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[1] != "/run/archiso/bootmnt" {
			continue
		}
		dev := f[0]
		if t := link(io, dev); t != "" { // /dev/disk/by-uuid/… → ../../sdb1
			dev = t
		}
		return wholeDisk(io, path.Base(dev))
	}
	if uuid := cmdlineArg(io, "archisosearchuuid"); uuid != "" {
		if t := link(io, "/dev/disk/by-uuid/"+uuid); t != "" {
			return wholeDisk(io, path.Base(t))
		}
	}
	return ""
}

// wholeDisk is the disk a block device is on: sdb1 → sdb, nvme0n1p1 → nvme0n1; a whole disk
// is itself.
func wholeDisk(io IO, name string) string {
	if io.Read("/sys/class/block/"+name+"/partition") != "" {
		name = strings.TrimRight(name, "0123456789")
		name = strings.TrimSuffix(name, "p") // nvme0n1p1, mmcblk0p1
	}
	return name
}

// cmdlineArg is a kernel command line argument's value ("" when it isn't there).
func cmdlineArg(io IO, key string) string {
	for _, f := range strings.Fields(io.Read("/proc/cmdline")) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// copyToRAM reports whether the system was copied into memory at boot (the boot menu's "copy
// to RAM" entry, copytoram=y), the same rule as the stick watcher's (internal/app/stick.go):
// then the OmaBoot? stick can come out and go into other ports.
func copyToRAM(io IO) bool {
	return strings.Contains(" "+io.Read("/proc/cmdline")+" ", " copytoram=y ")
}

// inputsUnder are the input devices on the USB device at dev or behind it (a hub's), from each
// device's sysfs link (…/usb3/3-2/3-2:1.0/0003:046D:C52B.0003/input/input12/event7).
func inputsUnder(io IO, dev string) []Device {
	var out []Device
	for _, d := range io.Inputs() {
		for _, c := range strings.Split(link(io, "/sys/class/input/"+d.Event), "/") {
			if isUSBDevicePath(c) && (c == dev || strings.HasPrefix(c, dev+".")) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// diskUnder is the disk on the USB device at dev or behind it: one with a medium first (a card
// reader's slot with a card), else the first; "" for none.
func diskUnder(io IO, disks map[string]string, dev string) string {
	var names []string
	for disk, on := range disks {
		if on == dev || strings.HasPrefix(on, dev+".") {
			names = append(names, disk)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if diskSize(io, n) > 0 {
			return n
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// bootStickUSB is the USB device the boot stick is, or "".
func bootStickUSB(io IO) string {
	if d := bootDisk(io); d != "" {
		return usbDisks(io)[d]
	}
	return ""
}

// diskSize is a block device's size in bytes (sysfs counts 512-byte sectors).
func diskSize(io IO, name string) int64 {
	n, _ := io.Int("/sys/block/" + name + "/size")
	return int64(n) * 512
}

// ── words ─────────────────────────────────────────────────────────────────────

// usbSpeed is a negotiated speed as the screen says it: "480 Mb/s (USB 2)".
func usbSpeed(s string) string {
	switch s {
	case "":
		return i18n.T("speed unknown")
	case "5000", "10000", "20000":
		n, _ := strconv.Atoi(s)
		return i18n.T("%[1]d Gb/s (%[2]s)", n/1000, usbGen(s))
	}
	return i18n.T("%[1]s Mb/s (%[2]s)", s, usbGen(s))
}

// usbRate is a negotiated speed alone, for the port map: "480 Mb/s", "5 Gb/s".
func usbRate(s string) string {
	switch s {
	case "":
		return "?"
	case "5000", "10000", "20000":
		n, _ := strconv.Atoi(s)
		return i18n.T("%d Gb/s", n/1000)
	}
	return i18n.T("%s Mb/s", s)
}

// usbSpeedEn is the same for the report, in English.
func usbSpeedEn(s string) string {
	if s == "" {
		return "speed unknown"
	}
	return fmt.Sprintf("%s Mb/s (%s)", s, usbGen(s))
}

// usbGen is the USB version a speed belongs to (a name, the same in every language).
func usbGen(s string) string {
	switch s {
	case "1.5", "12":
		return "USB 1.1"
	case "480":
		return "USB 2"
	case "5000":
		return "USB 3"
	case "10000":
		return "USB 3.1 Gen 2"
	case "20000":
		return "USB 3.2 Gen 2x2"
	}
	return "USB"
}

// fasterUSB reports whether speed a is above b.
func fasterUSB(a, b string) bool {
	x, _ := strconv.ParseFloat(a, 64)
	y, _ := strconv.ParseFloat(b, 64)
	return x > y
}

// gb is a size in GB, one decimal ("31.9 GB").
func gb(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/1e9) }
