// Package icons names the symbols omaboot-v carries beyond text: the ports drawn two cells wide
// and the Nerd Fonts icons, also two cells each, at our own private-use code points.
//
// The glyphs are made by tools/font/build.py; keep its lists in step with this file. The test
// checks every rune here exists in omaboot-v at 16, 24 and 32 px.
package icons

// The port symbols: a port's opening as seen on the Mac's side, the left and right half of a
// square two cells wide (private use, U+E200 + 2k and +1).
var (
	portUSBA        = [2]rune{'\ue200', '\ue201'}
	portUSBC        = [2]rune{'\ue202', '\ue203'} // a pill: USB-C and Thunderbolt 3
	portThunderbolt = [2]rune{'\ue204', '\ue205'} // Mini DisplayPort's shape: Thunderbolt 1 and 2
	portMagSafe     = [2]rune{'\ue206', '\ue207'}
	portHeadphone   = [2]rune{'\ue208', '\ue209'}
	portSD          = [2]rune{'\ue20a', '\ue20b'}
	portEthernet    = [2]rune{'\ue20c', '\ue20d'}
	portFireWire    = [2]rune{'\ue20e', '\ue20f'}
)

var ports = map[string][2]rune{
	"usb-a":            portUSBA,
	"usb-c":            portUSBC,
	"thunderbolt3":     portUSBC,
	"thunderbolt":      portThunderbolt,
	"mini-displayport": portThunderbolt,
	"magsafe":          portMagSafe,
	"magsafe2":         portMagSafe,
	"magsafe3":         portMagSafe,
	"headphone":        portHeadphone,
	"line-in":          portHeadphone, // the same 3.5 mm jack (0.7.3 review on the iMac10,1)
	"sd":               portSD,
	"ethernet":         portEthernet,
	"firewire400":      portFireWire,
	"firewire800":      portFireWire,
}

// Port is a port kind's two-cell symbol, left and right half; ok is false for a kind with none.
func Port(kind string) (sym [2]rune, ok bool) {
	sym, ok = ports[kind]
	return sym, ok
}

// The icons, drawn from Nerd Fonts (fa: Font Awesome; md: Material Design Icons, the glyph names)
// two cells wide: each is a left and a right half at our own code points
// U+E000 + 2i and + 2i + 1 in tools/font/build.py's order. The Linux console maps only 16-bit
// code points and shows U+F000-U+F1FF as raw glyph numbers, so the Nerd code points can't be used.
const (
	// Ports
	USB         = "\ue000\ue001" // fa-usb
	Thunderbolt = "\ue002\ue003" // md-lightning_bolt
	MagSafe     = "\ue004\ue005" // md-power_plug
	Headphones  = "\ue006\ue007" // md-headphones
	LineIn      = "\ue008\ue009" // md-microphone: the audio-in jack (0.7.4; it was md-audio_input_stereo_minijack)
	Optical     = "\ue00a\ue00b" // md-toslink
	SD          = "\ue00c\ue00d" // md-sd
	Ethernet    = "\ue00e\ue00f" // md-ethernet
	FireWire    = "\ue010\ue011" // md-firewire
	HDMI        = "\ue012\ue013" // md-video_input_hdmi
	VideoOut    = "\ue014\ue015" // md-monitor: Mini-DVI, DVI and other video outputs
	ExpressCard = "\ue016\ue017" // md-expansion_card_variant
	IR          = "\ue018\ue019" // md-remote
	Lock        = "\ue01a\ue01b" // md-lock: the security slot

	// Network
	WiFi = "\ue01c\ue01d" // fa-wifi

	// Categories and hardware
	Power     = "\ue01e\ue01f" // fa-power_off
	GPU       = "\ue020\ue021" // md-expansion_card
	Display   = "\ue022\ue023" // fa-desktop
	Keyboard  = "\ue024\ue025" // fa-keyboard_o
	Battery   = "\ue026\ue027" // fa-battery_three_quarters
	Speaker   = "\ue028\ue029" // fa-volume_up
	Mic       = "\ue02a\ue02b" // fa-microphone
	Camera    = "\ue02c\ue02d" // fa-camera
	Bluetooth = "\ue02e\ue02f" // fa-bluetooth
	Disk      = "\ue030\ue031" // fa-hdd_o
	Thermo    = "\ue032\ue033" // fa-thermometer_half
	Fan       = "\ue034\ue035" // md-fan
	Sleep     = "\ue036\ue037" // fa-moon_o
	Trackpad  = "\ue038\ue039" // md-trackpad
	Check     = "\ue03a\ue03b" // fa-check
	Warn      = "\ue03c\ue03d" // fa-warning
	Phone     = "\ue03e\ue03f" // md-cellphone_link: USB tethering
)

var portIcons = map[string]string{
	"usb-a":            USB,
	"usb-c":            USB,
	"thunderbolt":      Thunderbolt,
	"thunderbolt3":     Thunderbolt,
	"mini-displayport": Thunderbolt,
	"magsafe":          MagSafe,
	"magsafe2":         MagSafe,
	"magsafe3":         MagSafe,
	"headphone":        Headphones,
	"line-in":          LineIn,
	"optical":          Optical,
	"sd":               SD,
	"ethernet":         Ethernet,
	"firewire400":      FireWire,
	"firewire800":      FireWire,
	"hdmi":             HDMI,
	"mini-dvi":         VideoOut,
	"dvi":              VideoOut,
	"expresscard":      ExpressCard,
	"ir":               IR,
	"lock":             Lock,
	"micro-dvi":        VideoOut,
	"power":            Power,
	"line-out":         Headphones,
	"optical-out":      Optical,
	"optical-in":       Optical,
}

// PortIcon is a port kind's two-cell Nerd icon; ok is false for a kind with none.
func PortIcon(kind string) (icon string, ok bool) {
	icon, ok = portIcons[kind]
	return icon, ok
}

// ConsoleKey is the hint bar's key cap for the console key: the ` drawn larger and centred, as
// the font's own sits small at the cell's top edge (tools/font/build.py key_grave). Typing ` is
// unchanged.
const ConsoleKey = ''
