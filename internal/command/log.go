package command

import (
	"regexp"
	"sort"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// kernelTail is how much of the kernel log "log" shows without a filter: a boot logs a thousand
// lines and more.
const kernelTail = 100

func init() {
	Register(Cmd{Name: "log", Args: i18n.M("[filter]"), Help: i18n.M("the full log and the kernel log, filtered"), Order: 30,
		Complete: func(env Env, args []string) []string {
			if len(args) == 0 {
				return Drivers(env.KernelLog())
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			f := strings.ToLower(strings.Join(args, " "))
			match := func(l string) bool { return f == "" || strings.Contains(strings.ToLower(l), f) }
			n := 0
			for _, l := range env.Log() {
				if match(l) {
					out.Line(T(l))
					n++
				}
			}
			kl := env.KernelLog()
			var shown []string
			for _, l := range kl {
				if match(l) {
					shown = append(shown, l)
				}
			}
			if f == "" && len(shown) > kernelTail {
				out.Dim(i18n.T("kernel log: the last %d of %d lines; log <filter> narrows it", kernelTail, len(shown)))
				shown = shown[len(shown)-kernelTail:]
			}
			for _, l := range shown {
				out.Line(KernelLine(l)...)
			}
			if n+len(shown) == 0 {
				out.Dim(i18n.T("nothing matches %s", f))
			}
			return nil
		}})
}

// kline splits a kernel log line: "[    1.207] " and its driver ("i915 0000:00:02.0:", "applesmc:",
// "[drm]"), then the rest.
var kline = regexp.MustCompile(`^(\[[^\]]*\] )?(\[\w+\]|[\w.-]+(?: [\w:.-]+)?:)(.*)$`)

var (
	errWords  = regexp.MustCompile(`(?i)\berror\b|\bfail|\bhang`)
	warnWords = regexp.MustCompile(`(?i)\bwarn`)
)

// KernelLine colours a kernel log line: the time dim, the driver blue, the rest grey,
// amber for a warning, red for an error.
func KernelLine(l string) Line {
	m := kline.FindStringSubmatch(l)
	if m == nil {
		if ts, rest, ok := strings.Cut(l, "] "); ok && strings.HasPrefix(l, "[") { // no driver: "Linux version …"
			return Line{S(ts+"] ", Label), T(rest)}
		}
		return Line{T(l)}
	}
	c := g.Color(Text)
	switch {
	case errWords.MatchString(m[3]):
		c = Bad
	case warnWords.MatchString(m[3]):
		c = Mark
	}
	return Line{S(m[1], Label), S(m[2], Arg), S(m[3], c)}
}

// Drivers are the kernel log's 16 commonest drivers (their first word), for log's completion.
func Drivers(kl []string) []string {
	n := map[string]int{}
	for _, l := range kl {
		if m := kline.FindStringSubmatch(l); m != nil {
			d := strings.Trim(strings.Fields(m[2])[0], "[]:")
			if d != "" {
				n[d]++
			}
		}
	}
	out := make([]string, 0, len(n))
	for d := range n {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if n[out[i]] != n[out[j]] {
			return n[out[i]] > n[out[j]]
		}
		return out[i] < out[j]
	})
	out = out[:min(len(out), 16)]
	sort.Strings(out) // Tab lists them in order
	return out
}
