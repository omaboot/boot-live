package app

import (
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/network"
)

// tetherPhone is the phone sharing its connection over USB, for the USB port tests: the one the
// run is online through when there's one, else the first; nil when no phone is on USB.
func (r *Run) tetherPhone() *handson.Phone {
	r.mu.Lock()
	cur := r.net
	r.mu.Unlock()
	var ph *handson.Phone
	for _, l := range network.Links(r.Opt.Root) {
		if l.Kind != network.Tether || l.USB == "" {
			continue
		}
		on := cur.Online && cur.Iface == l.Name
		if ph != nil && !on {
			continue
		}
		ph = &handson.Phone{USB: l.USB, Name: l.Product, Online: on,
			Apple: l.Driver == "ipheth" || r.Opt.Root.Read("/sys/bus/usb/devices/"+l.USB+"/idVendor") == "05ac"}
		if on {
			break
		}
	}
	return ph
}
