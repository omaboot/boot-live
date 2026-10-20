package handson

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The light keys are found among hundreds by their names alone (the Air's 0.7.9 run: reading
// every key's data never finished and logged a kernel error per unreadable key).
func TestLightKeysByName(t *testing.T) {
	f := NewFake()
	for i := range 420 { // sorted, as the SMC lists them, with the AL keys among them
		f.SMC = append(f.SMC, FakeKey{Name: fmt.Sprintf("A%03d", i), Type: "ui8 "})
	}
	f.SMC = append(f.SMC, FakeKey{"ALI0", "{ali", nil}, FakeKey{"ALSL", "ui8 ", nil}, FakeKey{"ALV0", "{alv", nil}, FakeKey{"ALV1", "{alv", nil}, FakeKey{"B0AC", "si16", nil})
	slices.SortFunc(f.SMC, func(a, b FakeKey) int { return strings.Compare(a.Name, b.Name) })
	f.Set("/sys/bus/acpi/devices/APP0001:00/key_count", fmt.Sprint(len(f.SMC)))
	_, keys, _, err := lightKeys(f)
	if err != nil || !slices.Equal(keys, []string{"ALV0", "ALV1", "ALI0", "ALSL"}) {
		t.Fatalf("%v %v", keys, err)
	}
	if n := len(f.Writes); n > 16 {
		t.Errorf("%d key reads to find them", n)
	}
	k, err := ReadSMC(f, "ALV1")
	if err != nil || k.Name != "ALV1" {
		t.Fatalf("%+v %v", k, err)
	}
	if _, err := ReadSMC(f, "ZZZZ"); err == nil {
		t.Error("found a key that isn't there")
	}
}
