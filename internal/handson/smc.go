package handson

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Raw SMC keys, read through applesmc's key_at_index interface (0.7.2 review: the iMac10,1's
// light reading never moved, covered or lit, and applesmc's light file reads a 10-byte ALV0
// the MacBook way, bytes 6–7, with right = 0). The interface is four sysfs files next to the
// light file: writing an index to key_at_index picks a key (a variable in the driver, nothing
// sent to the SMC), and key_at_index_name, _type and _data read that key's name, type and raw
// bytes from the SMC. Nothing is ever written to the SMC itself.
//
// The index of a key is found by a binary search over the names (0.7.12, the Air's 0.7.9 run):
// the SMC lists its keys sorted, which applesmc's own lookups rely on, so about 10 names find
// the light keys among the Air's 424. Only names are read while searching: reading every key's
// data took too long for the test (it never finished) and logged "read data[0] fail" for each
// key that can't be read. Names read are kept per applesmc directory. Writing the index and
// reading behind it go together under smcMu, so the light test and the console's smc command
// can't pick each other's key.

// SMCKey is one SMC key as read: its name ("ALV0"), type ("{alv", "ui16"), and raw bytes.
type SMCKey struct {
	Name, Type string
	Data       []byte
}

// Hex is the bytes as "00 1f 03", or "(none)".
func (k SMCKey) Hex() string {
	if len(k.Data) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, c := range k.Data {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02x", c)
	}
	return b.String()
}

var (
	smcMu  sync.Mutex
	smcIdx = map[any]map[int]string{} // the names read, by index, per Mac and applesmc directory (cacheKey)
)

// cacheKey is where smcIdx keeps a Mac's names: the Mac's own (System) by its root, whichever
// System reads it; any other IO (a demo or test Fake) by itself.
func cacheKey(io IO, dir string) any {
	if s, ok := io.(*System); ok {
		return "system:" + string(s.Root) + ":" + dir
	}
	return struct {
		io  IO
		dir string
	}{io, dir}
}

// smcDir is applesmc's directory with the key_at_index interface: an ACPI device on recent
// kernels, a platform device on older ones (as lightFile), or "".
func smcDir(io IO) string {
	for _, pat := range []string{"/sys/bus/acpi/devices/APP0001:*", "/sys/devices/platform/applesmc.*"} {
		for _, d := range io.Glob(pat) {
			if io.Read(d+"/key_count") != "" {
				return d
			}
		}
	}
	return ""
}

// errNoSMC: no applesmc with the key_at_index interface.
var errNoSMC = errors.New("no applesmc key interface (key_count, key_at_index)")

// smcAt reads the key at index i (smcMu held).
func smcAt(io IO, dir string, i int) (SMCKey, error) {
	if err := io.Write(dir+"/key_at_index", strconv.Itoa(i)); err != nil {
		return SMCKey{}, err
	}
	k := SMCKey{Name: io.Read(dir + "/key_at_index_name"), Type: io.Read(dir + "/key_at_index_type"), Data: io.Bytes(dir + "/key_at_index_data")}
	if k.Name == "" {
		return k, fmt.Errorf("no key at index %d", i)
	}
	if n, ok := io.Int(dir + "/key_at_index_data_length"); ok && n < len(k.Data) {
		k.Data = k.Data[:n]
	}
	return k, nil
}

// smcName is the name of the key at index i, read without its data (smcMu held).
func smcName(io IO, dir string, i int) (string, error) {
	names := smcIdx[cacheKey(io, dir)]
	if names == nil {
		names = map[int]string{}
		smcIdx[cacheKey(io, dir)] = names
	}
	if n, ok := names[i]; ok {
		return n, nil
	}
	if err := io.Write(dir+"/key_at_index", strconv.Itoa(i)); err != nil {
		return "", err
	}
	n := io.Read(dir + "/key_at_index_name")
	if n == "" {
		return "", fmt.Errorf("no key at index %d", i)
	}
	names[i] = n
	return n, nil
}

// smcFrom is the first index whose key name sorts at or after name, of count keys (binary
// search: the SMC lists its keys sorted).
func smcFrom(io IO, dir, name string) (int, int, error) {
	smcMu.Lock()
	defer smcMu.Unlock()
	count, ok := io.Int(dir + "/key_count")
	if !ok {
		return 0, 0, errNoSMC
	}
	count = min(count, 4096)
	lo, hi := 0, count
	for lo < hi {
		mid := (lo + hi) / 2
		n, err := smcName(io, dir, mid)
		if err != nil {
			return 0, 0, err
		}
		if n < name {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, count, nil
}

// ReadSMC reads one key's type and raw bytes by its name (4 characters, "ALV0"): the console's
// smc command.
func ReadSMC(io IO, name string) (SMCKey, error) {
	dir := smcDir(io)
	if dir == "" {
		return SMCKey{}, errNoSMC
	}
	i, count, err := smcFrom(io, dir, name)
	if err != nil {
		return SMCKey{}, err
	}
	if i < count {
		if k, err := smcRead(io, dir, i); err == nil && k.Name == name {
			return k, nil
		}
	}
	return SMCKey{}, fmt.Errorf("the SMC has no key %s (%d keys)", name, count)
}

// smcRead reads the key at index i now.
func smcRead(io IO, dir string, i int) (SMCKey, error) {
	smcMu.Lock()
	defer smcMu.Unlock()
	return smcAt(io, dir, i)
}

// lightKeys are the SMC's light keys (names starting AL: ALV0, ALV1, ALSL, ALRV …, the ALV ones
// first) and their indexes, at most 6, found by their names alone.
func lightKeys(io IO) (dir string, keys []string, idx []int, err error) {
	dir = smcDir(io)
	if dir == "" {
		return "", nil, nil, errNoSMC
	}
	from, count, err := smcFrom(io, dir, "AL")
	if err != nil {
		return dir, nil, nil, err
	}
	var names []string
	smcMu.Lock()
	for i := from; i < count && len(names) < 32; i++ {
		n, err := smcName(io, dir, i)
		if err != nil || !strings.HasPrefix(n, "AL") {
			break
		}
		names = append(names, n)
	}
	smcMu.Unlock()
	for _, alv := range []bool{true, false} { // the light readings (ALV0, ALV1) first
		for i, n := range names {
			if strings.HasPrefix(n, "ALV") == alv && len(keys) < 6 {
				keys, idx = append(keys, n), append(idx, from+i)
			}
		}
	}
	return dir, keys, idx, nil
}

// FakeKey is a key on the demo's SMC (Fake.SMC).
type FakeKey struct {
	Name, Type string
	Data       []byte
}

// fakeSMC answers a write to key_at_index the way applesmc does: the key's name, type, length
// and bytes in the files next to it.
func (f *Fake) fakeSMC(p, v string) {
	i, err := strconv.Atoi(v)
	dir := strings.TrimSuffix(p, "/key_at_index")
	if err != nil || i < 0 || i >= len(f.SMC) {
		for _, s := range []string{"_name", "_type", "_data", "_data_length"} {
			delete(f.Files, p+s)
		}
		return
	}
	k := f.SMC[i]
	f.Files[dir+"/key_at_index_name"] = k.Name
	f.Files[dir+"/key_at_index_type"] = k.Type
	f.Files[dir+"/key_at_index_data"] = string(k.Data)
	f.Files[dir+"/key_at_index_data_length"] = strconv.Itoa(len(k.Data))
}
