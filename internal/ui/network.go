package ui

import (
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/network"
)

// ── Network ────────────────────────────────────────────────────────────────────
//
// Three views on one step: the list (Wi-Fi networks, then the actions), online (the connection
// and what next), and "Set static IP address" (a form). Above both, the found lines: Ethernet,
// a phone or modem, Wi-Fi. One cursor runs over the lines it can sit on, then the networks, then
// the actions: Enter on a line that's ready or off asks, then switches to it;
// on Wi-Fi it shows the networks. Going back to the step later keeps the connection.

// netJoining reports whether the step is busy getting online.
func netJoining(n NetState) bool { return n.Phase == NetJoining || n.Phase == NetDHCP }

// netBusy reports whether the step is busy: getting online, setting an address, switching.
func netBusy(n NetState) bool {
	return netJoining(n) || n.Phase == NetApplying || n.Phase == NetSwitching
}

// The found lines, top to bottom.
const (
	LineEthernet = iota
	LinePhone
	LineWiFi
)

// lineVia is the connection each line stands for, as NetState.Via names it.
var lineVia = [3]string{"Ethernet", "Phone", "Wi-Fi"}

// linePick reports whether the cursor can sit on line i: a connection that's ready or off (Enter
// switches to it), or Wi-Fi when it isn't in use and has networks to show.
func linePick(st *State, i int) bool {
	n := st.Net
	switch i {
	case LineEthernet:
		return n.EthLink == LinkReady || n.EthLink == LinkOff
	case LinePhone:
		return n.TetLink == LinkReady || n.TetLink == LinkOff
	case LineWiFi:
		return n.CanScan && !(n.Online && n.Via == "Wi-Fi") && (netUpView(st) || len(n.Networks) > 0)
	}
	return false
}

// lineStep is the next line the cursor can sit on from line i in direction d, or -1.
func lineStep(st *State, i, d int) int {
	for k := i + d; k >= LineEthernet && k <= LineWiFi; k += d {
		if linePick(st, k) {
			return k
		}
	}
	return -1
}

// PhoneName is the phone line's device as the lines and Boot name it: its USB product string
// ("iPhone", "Pixel 7", "Huawei E3372"), or "phone".
func PhoneName(n NetState) string {
	if n.Phone != "" {
		return n.Phone
	}
	return i18n.T("phone")
}

// Used is a connection as "off while … is used" and "back on …" name it: "the cable", "the
// iPhone", "Wi-Fi".
func Used(n NetState, via string) string {
	switch via {
	case "Ethernet":
		return i18n.T("the cable")
	case "Phone":
		return i18n.T("the %s", PhoneName(n))
	}
	return i18n.T("Wi-Fi")
}

// OffText is a line's text while it's off for the connection carrying the run (or the one
// being switched to, NetState.To): "enp0s20u1: off while the iPhone is used".
func OffText(n NetState, name string) string {
	via := n.Via
	if n.To != "" || !n.Online {
		via = n.To
	}
	return i18n.T("%s: off while %s is used", name, Used(n, via))
}

// connName is a connection as the switch question's heading and actions name it: "Ethernet",
// "the iPhone", "Wi-Fi".
func connName(n NetState, via string) string {
	if via == "Ethernet" {
		return i18n.T("Ethernet")
	}
	return Used(n, via)
}

// connOff is what switching away from a connection turns off: "the cable", "the iPhone's
// connection", "Wi-Fi".
func connOff(n NetState, via string) string {
	if via == "Phone" {
		return i18n.T("the %s's connection", PhoneName(n))
	}
	return Used(n, via)
}

// connOver is what the rest of the run goes over after switching: "the cable", "your iPhone",
// "Wi-Fi".
func connOver(n NetState, via string) string {
	if via == "Phone" {
		return i18n.T("your %s", PhoneName(n))
	}
	return Used(n, via)
}

// lineName is a found line's name: "Ethernet", "Phone or modem", "Wi-Fi".
func lineName(via string) string {
	switch via {
	case "Ethernet":
		return i18n.T("Ethernet")
	case "Phone":
		return i18n.T("Phone or modem")
	}
	return i18n.T("Wi-Fi")
}

// netUpView reports whether the step shows the online view.
func netUpView(st *State) bool {
	return st.Net.Online && !st.Browse && !netJoining(st.Net) && st.Net.Phase != NetApplying
}

func secWord(n WifiNet) string {
	if n.Security == "open" {
		return i18n.T("open, no password")
	}
	return i18n.T("%s, asks for its password", i18n.S(n.Security))
}

// listActions are the list view's actions.
func listActions(st *State) []Action {
	n := st.Net
	join := Action{Label: i18n.T("Join the selected network"), Does: i18n.T("pick a network above first"), Off: true}
	if st.NetPick >= 0 && st.NetPick < len(n.Networks) {
		w := n.Networks[st.NetPick]
		join = Action{Label: join.Label, Does: w.SSID + "  ·  " + secWord(w)}
	}
	scan := Action{Label: i18n.T("Scan again"), Does: i18n.T("look for networks again")}
	if !n.CanScan {
		scan.Off, scan.Does = true, i18n.T("no Wi-Fi to scan with")
	}
	return []Action{join, scan,
		{Label: i18n.T("Set static IP address"), Does: i18n.T("address, gateway and DNS, for networks without DHCP")},
		{Label: i18n.T("Skip, test offline"), Does: i18n.T("save the report to external storage, send it later")}}
}

// upActions are the online view's actions. Switching goes through the found lines above them.
func upActions(st *State) []Action {
	next := i18n.T("start the quick pass: no questions, about 2 minutes")
	switch {
	case st.PassDone:
		next = i18n.T("back to the results")
	case st.Net.Via == "Ethernet":
		next = i18n.T("online through the cable; start the quick pass")
	case st.Net.Via == "Phone":
		next = i18n.T("online through the %s; start the quick pass", PhoneName(st.Net))
	}
	cont := Action{Label: i18n.T("Continue"), Does: next}
	static := Action{Label: i18n.T("Set static IP address"), Does: i18n.T("address, gateway and DNS")}
	if st.Net.Via == "Wi-Fi" {
		return []Action{cont, {Label: i18n.T("Use another network"), Does: i18n.T("back to the list")}, static}
	}
	return []Action{cont, static, {Label: i18n.T("Skip, test offline"), Does: i18n.T("save the report to external storage, send it later")}}
}

// switchTo is the connection the switch question is about.
func switchTo(st *State) string {
	if st.SwitchVia == "" {
		return "Wi-Fi"
	}
	return st.SwitchVia
}

// drawSwitch asks before leaving a working connection for another (a line that's ready or off,
// or a Wi-Fi network): the other one then carries the run alone, so whatever is sent goes over
// it, and the old one is turned off, not unplugged.
func drawSwitch(gr *g.Grid, o *Options, b Box, st *State) {
	n, to := st.Net, switchTo(st)
	head := i18n.T("Switch to %s?", connName(n, to))
	lose := i18n.T("You're online through %s. Switching turns %s off, so the rest of the run goes over %s.",
		connName(n, n.Via), connOff(n, n.Via), connOver(n, to))
	if st.SwitchVia == "Wi-Fi" || st.SwitchVia == "" {
		ssid := ""
		if st.SwitchTo < len(n.Networks) {
			ssid = n.Networks[st.SwitchTo].SSID
		}
		lose = i18n.T("You're online through %s. Joining %s turns %s off, so the rest of the run goes over Wi-Fi.",
			connName(n, n.Via), ssid, connOff(n, n.Via))
	}
	drawAsk(gr, o, b, st, boot.Idle, head, lose, i18n.T("The %s line switches back any time.", lineName(n.Via)),
		switchActions(st), st.SwitchSel, g.VIO, g.WHT)
}

func switchActions(st *State) []Action {
	n := st.Net
	return []Action{{Label: i18n.T("Switch to %s", connName(n, switchTo(st))), Does: i18n.T("%s turns off; nothing to unplug", connOff(n, n.Via))},
		{Label: i18n.T("Stay on %s", connName(n, n.Via)), Does: i18n.T("back to the list")}}
}

// netBack reports whether Backspace goes back on the Network step now (it edits a field that
// has the focus, and waits while the Mac is getting online).
func netBack(st *State) bool {
	switch {
	case st.Form.Open:
		return st.Form.Focus >= 3 && st.Net.Phase != NetApplying
	case st.Pw:
		return false // the box's keys: Esc goes back
	}
	return !netBusy(st.Net)
}

// netSay is Boot's line on the Network step and when he started it. What the phone line has
// to say comes first while it's the newest news.
func netSay(st *State) (string, float64) {
	n := st.Net
	if say := phoneSay(n); say != "" && (n.PhoneAt >= n.At || !n.Online) && !netBusy(n) {
		return say, n.PhoneAt
	}
	switch {
	case n.Phase == NetSwitching:
		return i18n.T("switching to %s. a few seconds.", connName(n, n.To)), n.At
	case n.Online && n.SignIn:
		return i18n.T("this network wants a sign-in page. I have no browser: try another network."), n.At
	case n.Online && n.Static:
		return i18n.T("online with the address you gave me, %s.", n.IP), n.At
	case n.Online && n.Via == "Wi-Fi" && n.IP != "":
		return i18n.T("we're online. DHCP says %s.", n.IP), n.At
	case n.Online && n.Via == "Phone" && n.IP != "":
		return i18n.T("online through the %s. DHCP says %s.", PhoneName(n), n.IP), n.At
	case n.Online && n.IP != "":
		return i18n.T("online over the cable. DHCP says %s.", n.IP), n.At
	case n.Online:
		return i18n.T("we're online."), n.At
	case n.Err != "":
		return i18n.T("that didn't work. try again, or skip."), n.At
	case netJoining(n):
		return i18n.T("joining %s. a few seconds.", n.SSID), n.At
	case st.Pw:
		return i18n.T("type the password. I only see dots."), n.At
	case n.EthLink == LinkCarrier:
		return i18n.T("a cable is in. asking it for an address."), n.At
	case n.Phase == NetList && len(n.Networks) > 0:
		return i18n.N("pick yours. %d network in range.", "pick yours. %d networks in range.", len(n.Networks), len(n.Networks)), n.ListAt + 0.4
	case n.WifiChip == "" && n.Phase >= NetList:
		return i18n.T("no cable, no Wi-Fi. we can test offline."), 0.6
	}
	return i18n.T("no cable, no phone. Wi-Fi it is."), 0.6
}

// phoneSay is Boot's line about the phone line, or "": what to do on the phone, or that it's
// ready beside the cable.
func phoneSay(n NetState) string {
	name := PhoneName(n)
	switch {
	case n.TetLink == LinkLimit:
		return i18n.T("an LTE modem. it needs dialing, and I can't dial yet. a phone, Wi-Fi or a cable works.")
	case n.TetLink == LinkReady && n.Online && n.Via == "Ethernet":
		return i18n.T("your %[1]s's ready too. the cable stays unless you pick the %[1]s.", name)
	case n.TetLink != LinkCarrier:
		return ""
	case n.PhoneNeed == network.NeedTrust:
		return i18n.T("a phone turned up. tap Trust on it, then type its passcode.")
	case n.PhoneNeed == network.NeedTethering:
		return i18n.T("a %s. unlock it and turn on USB tethering.", name)
	case n.PhoneNeed == network.NeedModeswitch:
		return i18n.T("an LTE stick. switching it from CD to network.")
	case n.PhoneApple:
		return i18n.T("Personal Hotspot is on. asking it for an address.")
	}
	return i18n.T("USB tethering is on. asking it for an address.")
}

func netIntro(gr *g.Grid, o *Options, l Layout) {
	top := l.BandTop + 2
	if o.Plain {
		top = l.BandTop
	}
	gr.Put(l.FieldX, top, i18n.T("Getting online"), g.WHT)
	gr.Put(l.FieldX, top+1, i18n.T("To send your report. Offline works too: save the report and send it later."), g.DIM)
}

// linkColors are a found line's name and text colours by its link state.
func linkColors(state int) (g.Color, g.Color) {
	switch state {
	case LinkConnected:
		return g.GRY, g.WHT
	case LinkNone:
		return g.DIM, g.DIM
	}
	return g.GRY, g.GRY
}

// netValX is where the Found lines' values (and the Wi-Fi networks heading's note) start: past the
// ▶ column and the longest name with its icon.
const netValX = 20

// netFound draws the Ethernet, phone-or-modem and Wi-Fi lines; they follow the links live. cur is
// the line with the ▶ (-1: none). The one in use ends with "■ active"; one that's ready or off
// says what picking it does.
func netFound(gr *g.Grid, x, y0 int, t float64, ns NetState, cur, end int) {
	spin := spinner(t)
	wifi := i18n.N("%s, %d network", "%s, %d networks", len(ns.Networks), ns.WifiChip, len(ns.Networks))
	switch {
	case ns.WifiChip == "" && ns.NoWifi != "":
		wifi = ns.NoWifi
	case ns.WifiChip == "":
		wifi = i18n.T("no Wi-Fi card")
	case ns.Phase == NetScanning && ns.CanScan:
		wifi = i18n.T("%s, scanning %s", ns.WifiChip, spin)
	case ns.Parked == "Wi-Fi" && ns.Online:
		wifi = OffText(ns, ns.WifiChip)
	case ns.Online && ns.Via != "Wi-Fi" && len(ns.Networks) == 0:
		wifi = i18n.T("%s, not needed", ns.WifiChip)
	case ns.NoWifi != "":
		wifi = ns.WifiChip + ", " + ns.NoWifi
	}
	eth, tet := ns.Ethernet, ns.Tether
	if ns.EthLink == LinkCarrier && (!ns.Online || ns.Phase == NetSwitching) {
		eth += " " + spin
	}
	if ns.TetLink == LinkCarrier && ns.PhoneNeed != network.NeedTethering {
		tet += " " + spin
	}
	wl := LinkNone // Wi-Fi's line state, for its colours and what picking it does
	switch {
	case ns.Online && ns.Via == "Wi-Fi" && ns.Phase != NetSwitching:
		wl = LinkConnected
	case ns.Parked == "Wi-Fi" && ns.Online:
		wl = LinkOff
	case ns.WifiChip != "" && !(ns.Online && ns.Via != "Wi-Fi"):
		wl = LinkCarrier
	}
	lines := []struct {
		at               float64
		icon, name, text string
		link             int
	}{
		{0.3, icons.Ethernet, i18n.T("Ethernet"), eth, ns.EthLink},
		{0.7, icons.Phone, i18n.T("Phone or modem"), tet, ns.TetLink},
		{1.1, icons.WiFi, i18n.T("Wi-Fi"), wifi, wl},
	}
	for i, fl := range lines {
		if t < fl.at {
			continue
		}
		y, here := y0+i, i == cur
		nc, tc := linkColors(fl.link)
		if i == LineWiFi {
			tc = g.DIM
		}
		ic := nc // each row's icon in its state's colour, bright cyan for the one in use
		if here {
			gr.Put(x, y, "▶", g.BVIO)
			nc, ic = g.WHT, g.WHT
		}
		if fl.link == LinkConnected {
			ic = g.BCYN
		}
		nx := gr.Put(x+2, y, fl.icon, ic) + 1
		gr.Put(nx, y, cut(fl.name, x+netValX-1-nx), nc)
		vx := x + netValX
		if fl.link == LinkLimit { // a known limitation: an LTE modem it can't dial
			vx = gr.Put(vx, y, "▲", g.YEL) + 1
		}
		text := fl.text
		switch {
		case fl.link == LinkReady && here:
			text += "  ·  " + i18n.T("Enter switches to it")
		case fl.link == LinkOff && ns.Online && ns.Phase != NetSwitching:
			text += "  ·  " + i18n.T("pick it to switch back")
		case i == LineWiFi && here && len(ns.Networks) > 0:
			text += "  ·  " + i18n.T("Enter shows them")
		case i == LineWiFi && here:
			text += "  ·  " + i18n.T("Enter looks for them")
		}
		ex := gr.Put(vx, y, cut(text, end-vx), tc)
		if fl.link == LinkConnected && ex+3+width(i18n.T("active"))+2 <= end {
			gr.Put(ex+3, y, "■", g.BVIO)
			gr.Put(ex+5, y, i18n.T("active"), g.GRY)
		}
	}
}

// netList draws the Wi-Fi networks from y (the heading) and returns how many rows of networks
// it shows. cur is the row with the ▶ cursor (-1: none); picked gets a dim • when the cursor is
// elsewhere. hint goes after the heading (Tab's hint).
func netList(gr *g.Grid, x, y int, t float64, ns NetState, cur, picked int, hint string, end int) int {
	gr.Put(x, y, i18n.T("Wi-Fi networks"), g.GRY)
	if len(ns.Networks) == 0 {
		what := ns.NoWifi
		switch {
		case ns.Phase == NetScanning && ns.CanScan:
			what = i18n.T("scanning…")
		case what == "" && ns.WifiChip == "":
			what = i18n.T("no Wi-Fi card")
		case what == "":
			what = i18n.T("none found")
		}
		gr.Put(x+netValX, y, what, g.DIM)
		return 0
	}
	shown := min(len(ns.Networks), 7)
	focus := cur
	if focus < 0 {
		focus = picked
	}
	top := max(0, min(focus-shown+1, len(ns.Networks)-shown))
	if len(ns.Networks) > shown { // a long list: which part of it is showing
		hint = strings.TrimSuffix(i18n.T("%d–%d of %d", top+1, top+shown, len(ns.Networks))+"  ·  "+hint, "  ·  ")
	}
	if hint != "" && t >= ns.ListAt {
		gr.Put(x+netValX, y, cut(hint, end-(x+netValX)), g.DIM)
	}
	for i := top; i < top+shown; i++ {
		if t < ns.ListAt+float64(i-top)*0.1 {
			continue
		}
		n := ns.Networks[i]
		yy := y + 1 + i - top
		here, pick := i == cur, cur < 0 && i == picked
		switch {
		case here:
			gr.Put(x, yy, "▶", g.BVIO)
		case pick:
			gr.Put(x, yy, "•", g.DIM) // the list hasn't the focus: its cursor dims
		}
		nc := g.GRY
		if here || pick {
			nc = g.WHT
		}
		ic := nc // the Wi-Fi icon, bright cyan on the network the Mac is on
		if ns.Online && ns.Via == "Wi-Fi" && n.SSID == ns.SSID {
			ic = g.BCYN
		}
		nx := gr.Put(x+2, yy, icons.WiFi, ic) + 1
		gr.Put(nx, yy, padTo(cut(n.SSID, 20), 20), nc)
		for k := 0; k < 4; k++ {
			if k < n.Bars {
				gr.Put(nx+21+k, yy, "■", g.CYN)
			} else {
				gr.Put(nx+21+k, yy, "·", g.DIM)
			}
		}
		gr.Put(nx+27, yy, cut(i18n.S(n.Security), end-(nx+27)), g.DIM)
	}
	return shown
}

// snapLine is the catalog snapshot line once online: checking (with a spinner), then what the
// check found, with a ✓ or × and the server, not dimmed; under it, dim, the snapshot built into
// the stick and whether it's the one in use. Its own rows, a blank one above.
func snapLine(gr *g.Grid, x, y int, t float64, ns NetState, end int) {
	if ns.Refreshed == "" || t <= ns.RefreshedAt {
		return
	}
	m, mc := "✓", g.BVIO
	switch {
	case ns.Checking:
		m = spinner(t)
	case ns.RefreshFailed:
		m, mc = "×", g.BRED
	}
	gr.Put(x, y, m, mc)
	gr.Put(x+2, y, cut(ns.Refreshed, end-(x+2)), g.GRY)
	if ns.Stick != "" && !ns.Checking {
		gr.Put(x+2, y+1, cut(ns.Stick, end-(x+2)), g.DIM)
	}
}

// netErr draws the last attempt's problem in the online view (a switch that failed, and the
// connection that came back), in the snapshot line's place; it reports whether there was one.
func netErr(gr *g.Grid, x, y int, ns NetState, end int) bool {
	msg := ns.Err
	if msg == "" && ns.Online && ns.SignIn {
		msg = i18n.T("this network wants a sign-in page first")
	}
	if msg == "" || ns.Phase == NetSwitching {
		return false
	}
	gr.Put(x, y, "×", g.BRED)
	gr.Put(x+2, y, cut(msg, end-(x+2)), g.GRY)
	return true
}

// netListY is where the list view's actions go: one blank line under the networks, and never
// beside the password box.
func netListY(ly, shown int) int { return ly + max(shown+2, 7) }

func drawNetwork(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t, ns := st.T, st.Net
	up := netUpView(st)
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Network"), step: 2, ho: st.HO, right: netRight(f, st), mac: true,
		keys: Keys{Esc: !st.Pw, Back: netBack(st)}})
	l := layout(b, o.Plain)
	sayY, sayW := l.BandTop+5, l.FieldW-1
	if o.Plain {
		netIntro(gr, o, l)
	} else { // the way out on the band's right
		sayY, sayW = netBand(gr, l, i18n.T("Getting online"), i18n.T("To send your report. Offline works too: save the report and send it later."), netPathOf(st, f), t)
	}
	if !o.Plain {
		ex := boot.Idle
		switch {
		case up && t < ns.At+2.2:
			ex = boot.Pleased
		case !ns.Online && (ns.Phase == NetScanning || netJoining(ns)), ns.Phase == NetApplying, ns.Phase == NetSwitching:
			ex = boot.Work
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
		say, at := netSay(st)
		next := ns.At + 2.0
		then := up && t > next && say != phoneSay(ns) && !ns.SignIn // what's next, unless the phone or a sign-in page has his word
		var ls []line
		if t > 0.6 {
			ls = append(ls, line{min(at, t), say})
		}
		if then {
			s := i18n.T("quick pass next. no questions, about 2 minutes.")
			if st.PassDone {
				s = i18n.T("your results are kept. Continue goes back to them.")
			}
			ls = append(ls, line{next, s})
		}
		sayBox(gr, &st.Said, l.FieldX, sayY, sayW, l.RuleY-sayY-2, t, ls, -1)
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	x, y0, end := b.X+1, l.ContentY, b.X+b.W
	netFound(gr, x, y0, t, ns, st.LineSel, end)
	ly := y0 + 4
	rx := b.X + 46
	if up {
		usel := st.UpSel // the actions' ▶, unless the lines have the cursor or a switch is going on
		if st.LineSel >= 0 || ns.Phase == NetSwitching {
			usel = -1
		}
		if ns.Via == "Wi-Fi" {
			picked := -1
			for i, n := range ns.Networks {
				if n.SSID == ns.SSID {
					picked = i
				}
			}
			shown := netList(gr, x, ly, t, ns, -1, picked, "", rx-2)
			gr.Put(rx, ly, "■", g.BVIO)
			gr.Put(rx+2, ly, i18n.T("Connected"), g.WHT)
			gr.Put(rx+2, ly+1, cut(ns.Info, end-(rx+2)), g.GRY)
			if !netErr(gr, rx, ly+3, ns, end) {
				snapLine(gr, rx+2, ly+3, t, ns, end)
			}
			actions(gr, x, netListY(ly, shown), upActions(st), usel, true, 0, end)
			return b
		}
		gr.Put(x, ly, i18n.T("Wi-Fi networks"), g.GRY)
		w := i18n.T("not needed now")
		switch {
		case len(ns.Networks) > 0:
			w = i18n.N("%d in range, not needed now", "%d in range, not needed now", len(ns.Networks), len(ns.Networks)) +
				"  ·  " + i18n.T("joining one tests Wi-Fi") // a scan alone leaves it not tested
		case !ns.CanScan:
			w = i18n.T("no Wi-Fi to scan with")
		}
		gr.Put(x+netValX, ly, cut(w, end-(x+netValX)), g.DIM)
		if !netErr(gr, x, ly+2, ns, end) {
			snapLine(gr, x, ly+2, t, ns, end)
		}
		actions(gr, x, ly+5, upActions(st), usel, true, 0, end)
		return b
	}
	// the list view: one cursor runs down the lines, the networks and on into the actions
	busy := st.Pw || netBusy(ns)
	onLines := st.LineSel >= 0 && !busy
	cur := -1
	if !busy && !onLines && st.NetSel < len(ns.Networks) {
		cur = st.NetSel
	}
	onActs := st.NetSel >= len(ns.Networks)
	hint := ""
	switch {
	case busy || len(ns.Networks) == 0:
	case onActs:
		hint = i18n.T("Tab goes back to the list")
	default:
		hint = i18n.T("Tab jumps to the actions below")
	}
	shown := 0
	if t > 1.1 {
		lend := end
		if st.Pw {
			lend = rx - 2 // the password box is beside the list
		}
		shown = netList(gr, x, ly, t, ns, cur, st.NetPick, hint, lend)
	}
	if t > 1.4 {
		// the actions' ▶ while they have the focus, a dim · where Tab goes back to while the
		// networks have it
		sel, active := st.NetSel-len(ns.Networks), !busy && onActs && !onLines
		if !onActs {
			sel = st.NetAct
		}
		if busy || onLines {
			sel = -1
		}
		actions(gr, x, netListY(ly, shown), listActions(st), sel, active, 0, end)
	}
	if st.Pw { // the Wi-Fi test's box; joining, its keys' row is the spinner's
		pw := st.PwShown
		if !st.PwShow {
			pw = make([]rune, st.PwLen) // dots: only the count
		}
		handson.PasswordBox(gr, rx, ly, end-rx, 5, t, i18n.T("Password for %s", ns.SSID), pw, st.PwShow, !netJoining(ns))
		gr.Put(rx, ly+5, cut(i18n.T("kept in memory only: never saved, never sent"), end-rx), g.DIM)
	}
	switch {
	case netJoining(ns):
		gr.Put(rx, ly+4, spinner(t), g.BVIO)
		s := i18n.T("asking for an address (DHCP)…")
		if ns.Phase == NetJoining {
			s = i18n.T("joining %s…", ns.SSID)
		}
		gr.Put(rx+2, ly+4, s, g.GRY)
	case ns.Err != "" && !st.Pw:
		gr.Put(rx, ly+4, "×", g.BRED)
		gr.Put(rx+2, ly+4, cut(ns.Err, end-(rx+2)), g.GRY)
	}
	return b
}

// ── Set static IP address ──────────────────────────────────────────────────────

// staticDone reports whether the form's settings are in use and the Mac is online with them.
func staticDone(st *State) bool {
	n := st.Net
	return st.Form.Requested && n.Phase != NetApplying && n.Online && n.Static && n.Err == ""
}

func staticActions(st *State, target StaticTarget) []Action {
	if staticDone(st) {
		next := i18n.T("start the quick pass")
		if st.PassDone {
			next = i18n.T("back to the results")
		}
		return []Action{{Label: i18n.T("Continue"), Does: next}, {Label: i18n.T("Change the settings"), Does: i18n.T("back to the fields")}}
	}
	apply := Action{Label: i18n.T("Apply"), Does: i18n.T("use these settings for this run")}
	if target.Iface == "" {
		apply.Off, apply.Does = true, i18n.T("plug in a cable or adapter, or join a Wi-Fi network first")
	}
	return []Action{apply, {Label: i18n.T("Cancel"), Does: i18n.T("back to the network list")}}
}

func drawStatic(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t, ns, fm := st.T, st.Net, st.Form
	applying := ns.Phase == NetApplying
	done := staticDone(st)
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Static IP address"), step: 2, ho: st.HO, right: netRight(f, st), mac: true,
		keys: Keys{Esc: true, Back: netBack(st)}})
	l := layout(b, o.Plain)
	head, note := i18n.T("Set a static IP address"), i18n.T("For networks without DHCP. Ask whoever runs the network for these.")
	sayY, sayW := l.BandTop+5, l.FieldW-1
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, head, g.WHT)
		gr.Put(l.FieldX, l.BandTop+1, note, g.DIM)
	} else { // the way out with the typed values on it
		sayY, sayW = netBand(gr, l, head, note, staticPathOf(st, f, applying, done), t)
	}
	target := f.Static
	if !o.Plain {
		ex := boot.Idle
		switch {
		case done && t < ns.At+2:
			ex = boot.Pleased
		case applying:
			ex = boot.Work
		case fm.Err != "" || fm.Requested && ns.Err != "":
			ex = boot.Concerned
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
		say, at := i18n.T("tell me the address and I'll use it."), 0.4
		switch {
		case done:
			say, at = i18n.T("that works. we're online."), ns.At
		case applying:
			say, at = i18n.T("setting it up. a few seconds."), ns.At
		case fm.Err != "" || fm.Requested && ns.Err != "":
			say, at = i18n.T("that didn't work. check the numbers."), ns.At
		case ns.EthLink == LinkCarrier:
			say = i18n.T("no address offered on this cable. tell me one and I'll use it.")
		}
		if t > 0.4 {
			sayBox(gr, &st.Said, l.FieldX, sayY, sayW, l.RuleY-sayY-2, t, []line{{min(at, t), say}}, -1)
		}
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	x, y0, end := b.X+1, l.ContentY, b.X+b.W
	if target.Iface == "" {
		gr.Put(x, y0, cut(target.What, end-x), g.GRY)
	} else {
		ix := gr.Put(x, y0, network.Shown(target.Iface, ""), g.GRY)
		gr.Put(ix, y0, cut("  ·  "+target.What, end-ix), g.DIM)
	}
	labels := []string{i18n.T("Address/prefix"), i18n.T("Gateway"), i18n.T("DNS")}
	helps := []string{i18n.T("for example 192.168.1.50/24"), "", i18n.T("one or more, separated by spaces")}
	const fw = 24
	for i, label := range labels {
		y, on := y0+2+i, fm.Focus == i && !applying && !done
		gr.Put(x, y, label, g.GRY)
		bc := g.DIM
		if on {
			bc = g.BVIO
		}
		gr.Put(x+16, y, "[", bc)
		gr.Put(x+16+fw+1, y, "]", bc)
		v := fm.Fields[i]
		if len(v) > fw-2 {
			v = v[len(v)-(fw-2):]
		}
		gr.Put(x+18, y, string(v), g.WHT)
		if on && blinkOn(t) {
			gr.Put(x+18+len(v), y, "_", g.BVIO)
		}
		if helps[i] != "" {
			gr.Put(x+16+fw+4, y, helps[i], g.DIM)
		}
	}
	gr.Put(x, y0+5, i18n.T("Tab moves to the next field. Kept for this run only: nothing is saved."), g.DIM)
	ay := y0 + 7
	switch {
	case applying:
		gr.Put(x, ay, spinner(t), g.BVIO)
		gr.Put(x+2, ay, i18n.T("applying: address, route, DNS… then a look for %s", f.Site), g.GRY)
		return b
	case done:
		gr.Put(x, ay, "■", g.BVIO)
		gr.Put(x+2, ay, i18n.T("%s: connected · %s", network.Shown(ns.Iface, ""), ns.IP), g.WHT)
		gr.Put(x+2, ay+1, i18n.T("route via %s  ·  DNS finds %s", ns.Gateway, f.Site), g.DIM)
		ay += 3
	default:
		msg := fm.Err
		if msg == "" && fm.Requested {
			msg = ns.Err
		}
		if msg != "" {
			gr.Put(x, ay, "×", g.BRED)
			gr.Put(x+2, ay, cut(msg, end-(x+2)), g.GRY)
			ay += 2
		}
	}
	actions(gr, x, ay, staticActions(st, target), fm.Focus-3, fm.Focus >= 3, 0, end)
	return b
}
