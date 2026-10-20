package handson

import (
	"reflect"
	"testing"
)

func TestWokenBy(t *testing.T) {
	names, armed := acpiWakeup("Device\tS-state\t  Status   Sysfs node\n" +
		"LID0\t  S4\t*enabled   platform:PNP0C0D:00\n" +
		"XHC1\t  S3\t*enabled   pci:0000:00:14.0\n" +
		"EHC1\t  S3\t*disabled  pci:0000:00:1d.0\n" +
		"ADP1\t  S4\t*disabled\n")
	if !reflect.DeepEqual(armed, []string{"LID0", "XHC1"}) || names["0000:00:14.0"] != "XHC1" {
		t.Fatalf("acpiWakeup: armed %v names %v", armed, names)
	}
	before := wakeSnap{sources: map[string]int{"0000:00:14.0": 4, "00:01": 2, "PNP0C0D:00": 1}, events: map[string]int{"gpe0d": 7, "ff_rt_clk": 2, "ff_pmtimer": 0}}
	after := wakeSnap{sources: map[string]int{"0000:00:14.0": 5, "00:01": 2, "PNP0C0D:00": 1, "new": 1}, events: map[string]int{"gpe0d": 8, "ff_rt_clk": 2, "ff_pmtimer": 9}}
	src, ev := wokenBy(before, after, names, "00:01")
	if !reflect.DeepEqual(src, []string{"XHC1 (0000:00:14.0)", "new"}) || !reflect.DeepEqual(ev, []string{"GPE 0x0D +1"}) {
		t.Fatalf("wokenBy: %v %v", src, ev)
	}
}
