package send

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"testing"
)

// disk is a sparse in-memory disk.
type disk map[int64][]byte

func (d disk) ReadAt(p []byte, off int64) (int, error) {
	for i := range p {
		s := (off + int64(i)) / sector
		if b, ok := d[s]; ok {
			p[i] = b[(off+int64(i))%sector]
		} else {
			p[i] = 0
		}
	}
	return len(p), nil
}

func (d disk) write(p []byte, off int64) {
	for i := range p {
		s := (off + int64(i)) / sector
		if _, ok := d[s]; !ok {
			d[s] = make([]byte, sector)
		}
		d[s][(off+int64(i))%sector] = p[i]
	}
}

// image lays a disk out like our ISOs (xorriso, 2026-10-10): MBR ISO + EFI slots, and a GPT with
// ISOHybrid, ISOHybrid1 and the EFI partition, its backup at the image's end.
func image(withGPT bool) disk {
	d := disk{}
	isoEnd, efiStart, efiCount := uint64(20480), uint64(20480), uint64(2048)
	imgEnd := efiStart + efiCount + 64 // sectors in the image
	mbr := make([]byte, sector)
	mbr[510], mbr[511] = 0x55, 0xaa
	for i, s := range []slot{{0, 64, uint32(isoEnd - 64)}, {0xef, uint32(efiStart), uint32(efiCount)}} {
		e := mbr[mbrEntries+16*i:]
		e[4] = s.Type
		binary.LittleEndian.PutUint32(e[8:], s.Start)
		binary.LittleEndian.PutUint32(e[12:], s.Count)
	}
	d.write(mbr, 0)
	if !withGPT {
		return d
	}
	n, sz := uint32(248), uint32(128)
	arr := make([]byte, n*sz)
	for i, p := range [][2]uint64{{64, isoEnd - 1}, {484, 559}, {efiStart, efiStart + efiCount - 1}} {
		e := arr[uint32(i)*sz:]
		copy(e, basicData)
		e[16] = byte(i + 1)
		binary.LittleEndian.PutUint64(e[32:], p[0])
		binary.LittleEndian.PutUint64(e[40:], p[1])
	}
	hdr := make([]byte, gptHdrSize)
	copy(hdr, "EFI PART")
	binary.LittleEndian.PutUint32(hdr[8:], 0x10000)
	binary.LittleEndian.PutUint32(hdr[12:], gptHdrSize)
	put64(hdr, 24, 1)
	put64(hdr, 32, imgEnd-1)
	put64(hdr, 40, 64)
	put64(hdr, 48, imgEnd-64)
	put64(hdr, 72, 2)
	binary.LittleEndian.PutUint32(hdr[80:], n)
	binary.LittleEndian.PutUint32(hdr[84:], sz)
	gpt{arr: arr}.seal(hdr)
	d.write(hdr, sector)
	d.write(arr, 2*sector)
	return d
}

func TestPlan(t *testing.T) {
	const total = uint64(2 << 30 / sector) // a 2 GiB stick
	for _, withGPT := range []bool{true, false} {
		d := image(withGPT)
		ws, room, err := Plan(d, total)
		if err != nil {
			t.Fatalf("gpt=%v: %v", withGPT, err)
		}
		// at the end, 1 MiB aligned, with at least 1 MiB past it for the GPT's backup
		start, count, at := room.Start, room.Count, uint64(4061184)
		if withGPT {
			at = 4059136
		}
		if start != at || count != roomMax || room.Found {
			t.Errorf("gpt=%v: partition at %d+%d (found %v), want %d+%d", withGPT, start, count, room.Found, at, roomMax)
		}
		for _, w := range ws {
			d.write(w.Data, w.Off)
		}
		// Linux reads the MBR: slot 3
		m := make([]byte, sector)
		d.ReadAt(m, 0)
		if e := m[mbrEntries+32:]; e[4] != fat16LBA || binary.LittleEndian.Uint32(e[8:]) != uint32(start) {
			t.Errorf("gpt=%v: MBR slot 3 = %x", withGPT, e[:16])
		}
		if !withGPT {
			continue
		}
		// Mac firmware and macOS read the GPT: both copies valid, entry 4 ours, backup at the end
		g, ok, err := readGPT(d)
		if !ok || err != nil {
			t.Fatalf("primary GPT after: %v %v", ok, err)
		}
		if e := g.entry(3); binary.LittleEndian.Uint64(e[32:]) != start || !bytes.Equal(e[:16], basicData) {
			t.Errorf("GPT entry 4 = %x", e[:48])
		}
		if g.u64(32) != total-1 || g.u64(48) != total-2-g.arraySectors() {
			t.Errorf("primary: backup at %d, last usable %d", g.u64(32), g.u64(48))
		}
		b := make([]byte, sector)
		d.ReadAt(b, int64(total-1)*sector)
		h := append([]byte(nil), b[:gptHdrSize]...)
		want := binary.LittleEndian.Uint32(h[16:])
		binary.LittleEndian.PutUint32(h[16:], 0)
		arr := make([]byte, len(g.arr))
		d.ReadAt(arr, int64(binary.LittleEndian.Uint64(b[72:]))*sector)
		if string(b[:8]) != "EFI PART" || crc32.ChecksumIEEE(h) != want || !bytes.Equal(arr, g.arr) || binary.LittleEndian.Uint64(b[24:]) != total-1 {
			t.Errorf("backup GPT at the end isn't valid")
		}
		// a second run finds no free MBR slot and leaves the stick alone
		if _, _, err := Plan(d, total); err == nil {
			t.Errorf("second run planned again")
		}
	}
	if _, _, err := Plan(image(true), 22528+1000); err == nil {
		t.Errorf("no room: planned anyway")
	}
	if _, _, err := Plan(disk{}, total); err == nil {
		t.Errorf("no MBR: planned anyway")
	}
}

// fatBoot is a boot sector as mkfs.fat -F 16 -n label writes it for a partition of n sectors.
func fatBoot(label string, n uint64) []byte {
	b := make([]byte, sector)
	b[0], b[510], b[511] = 0xeb, 0x55, 0xaa
	binary.LittleEndian.PutUint16(b[11:], sector)
	binary.LittleEndian.PutUint32(b[32:], uint32(n))
	b[38] = 0x29
	copy(b[43:], fmt.Sprintf("%-11s", label))
	copy(b[54:], "FAT16   ")
	return b
}

// A new image written over the stick leaves OMABOOT out of the table but in place: Plan finds it
// and puts it back where it is, with or without a GPT in the new image; anything else at the end
// gets a new one.
func TestPlanFindsRoom(t *testing.T) {
	const total = uint64(2 << 30 / sector)
	_, made, _ := Plan(image(true), total)
	for _, withGPT := range []bool{true, false} {
		d := image(withGPT) // the new image's write; mkfs.fat's volume is whole tracks of 63
		d.write(fatBoot(RoomLabel, made.Count-32), int64(made.Start)*sector)
		ws, room, err := Plan(d, total)
		if err != nil || !room.Found || room.Start != made.Start || room.Count != made.Count || len(ws) == 0 {
			t.Errorf("gpt=%v: %+v %v, want %d+%d found", withGPT, room, err, made.Start, made.Count)
		}
	}
	for _, b := range [][]byte{fatBoot("NO NAME", roomMax), fatBoot(RoomLabel, roomMax+1)} {
		d := image(true)
		d.write(b, int64(made.Start)*sector)
		if _, room, err := Plan(d, total); err != nil || room.Found {
			t.Errorf("%q: %+v %v, want a new one", b[43:54], room, err)
		}
	}
}
