package main

import (
	"fmt"

	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/ui"
)

// The Bluetooth test in a busy room (the demo's own three devices are in handson_ports.go): 22
// devices, most of them strangers' unnamed Low Energy beacons. The tester's iPhone is heard at
// 3 s without a name (its settings not open yet) and gets its name at 9 s, filling in on the
// list; a stranger's named laptop joins the list's end at 11 s. The list is up at 7 s; the
// answers are the page's devices, then Mine isn't listed (9), then More devices (0).
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			variantShot("h9-bt-busy-scanning", "network.bluetooth", 5, busyRoom),
			variantShot("h9-bt-busy-list", "network.bluetooth", 8, busyRoom),
			variantShot("h9-bt-busy-named", "network.bluetooth", 12, busyRoom),
			variantShot("h9-bt-busy-more", "network.bluetooth", 13, busyRoom, ui.Pick{At: 12.5, Action: 9}),
			variantShot("h9-bt-busy-pass", "network.bluetooth", 14, busyRoom, ui.Pick{At: 13, Action: 1}),
			variantShot("h9-bt-busy-mine-not-listed", "network.bluetooth", 33, busyRoom, ui.Pick{At: 31, Action: 9}, ui.Pick{At: 31.5, Action: 8}),
		}
	})
}

// busyRoom is the demo's adapter hearing a room full of devices.
func busyRoom(f *handson.Fake) {
	handson.FakeBluetoothDevice(f, 3, "hci0", handson.BTDevice{Key: "phone", Maker: "Apple", LE: true, RSSI: -44})
	handson.FakeBluetoothDevice(f, 9, "hci0", handson.BTDevice{Key: "phone", Name: "Carl’s iPhone", Kind: "phone", RSSI: -44})
	handson.FakeBluetoothDevice(f, 3.4, "hci0", handson.BTDevice{Key: "tv", Name: "Living Room TV", Kind: "audio", RSSI: -71})
	handson.FakeBluetoothDevice(f, 3.6, "hci0", handson.BTDevice{Key: "buds", Name: "Galaxy Buds2", Kind: "audio", LE: true, RSSI: -66})
	handson.FakeBluetoothDevice(f, 4.1, "hci0", handson.BTDevice{Key: "watch", Name: "Pixel Watch", Kind: "watch", LE: true, RSSI: -79})
	handson.FakeBluetoothDevice(f, 11, "hci0", handson.BTDevice{Key: "laptop", Name: "ThinkPad X1", Kind: "computer", RSSI: -58})
	makers := []string{"Apple", "Apple", "Samsung", "", "Apple", "Microsoft", "", "Apple", "Google", "", "Apple", "", "Bose", "Apple", "", "Apple", "Tile"}
	for i, m := range makers {
		handson.FakeBluetoothDevice(f, 3.2+0.2*float64(i), "hci0", handson.BTDevice{Key: fmt.Sprintf("le%02d", i), Maker: m, LE: true, RSSI: -50 - 3*i})
	}
}
