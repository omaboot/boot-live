// Package sysroot reads the system's /sys, /proc and /etc files under a root directory: "/" on
// a live-booted Mac, or a fixture tree (testdata/<Mac>/) for tests and for trying the real mode's
// probes on a development machine (--sysroot). Every probe reads through it, so a fixture tree
// stands in for the whole machine.
package sysroot

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Root is the directory the system's files are read under. "" and "/" are the running system.
type Root string

// System is the running system.
const System Root = "/"

// IsSystem reports whether r is the running system rather than a fixture tree.
func (r Root) IsSystem() bool { return r == "" || filepath.Clean(string(r)) == "/" }

// P is a system path under the root: P("/sys/class/net") → "<root>/sys/class/net".
func (r Root) P(p string) string {
	if r.IsSystem() {
		return p
	}
	return filepath.Join(string(r), p)
}

// Read returns a file's contents with surrounding white space trimmed, or "" if it can't be read.
func (r Root) Read(p string) string {
	b, err := os.ReadFile(r.P(p))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Bytes returns a file's contents, or nil.
func (r Root) Bytes(p string) []byte {
	b, err := os.ReadFile(r.P(p))
	if err != nil {
		return nil
	}
	return b
}

// Int reads a file holding one integer.
func (r Root) Int(p string) (int, bool) {
	s := r.Read(p)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// Hex reads a file holding one hex number such as "0x8086", as four lower-case digits.
func (r Root) Hex(p string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.ToLower(r.Read(p)), "0x"))
	if s == "" {
		return ""
	}
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// Exists reports whether a path exists (a dangling symlink counts).
func (r Root) Exists(p string) bool {
	_, err := os.Lstat(r.P(p))
	return err == nil
}

// IsDir reports whether a path is a directory.
func (r Root) IsDir(p string) bool {
	st, err := os.Stat(r.P(p))
	return err == nil && st.IsDir()
}

// Glob returns the system paths (without the root) matching a pattern, sorted.
func (r Root) Glob(pattern string) []string {
	ms, _ := filepath.Glob(r.P(pattern))
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if r.IsSystem() {
			out = append(out, m)
			continue
		}
		rel, err := filepath.Rel(string(r), m)
		if err == nil {
			out = append(out, "/"+rel)
		}
	}
	sort.Strings(out)
	return out
}

// Link returns a symlink's target as written, or "".
func (r Root) Link(p string) string {
	t, err := os.Readlink(r.P(p))
	if err != nil {
		return ""
	}
	return t
}

// Uevent reads a sysfs uevent file into its KEY=value pairs.
func (r Root) Uevent(p string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(r.Read(p), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			m[k] = v
		}
	}
	return m
}

// Lines reads a file's non-empty lines.
func (r Root) Lines(p string) []string {
	var out []string
	for _, l := range strings.Split(r.Read(p), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
