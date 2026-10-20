// Package audio holds the speaker test's spoken words: one short English word per WAV (16-bit
// mono 22,050 Hz), made once with Piper TTS and its US English Amy voice (see NOTICE),
// built into the binary. The beeps are made at run time, not shipped.
package audio

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.wav NOTICE
var files embed.FS

// Words are the clips' words, sorted.
func Words() []string {
	es, _ := files.ReadDir(".")
	var out []string
	for _, e := range es {
		if w, ok := strings.CutSuffix(e.Name(), ".wav"); ok {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// Clip is a word's WAV file, or nil.
func Clip(word string) []byte {
	b, err := files.ReadFile(word + ".wav")
	if err != nil {
		return nil
	}
	return b
}

// Notice is the clips' licence notice.
func Notice() string {
	b, _ := files.ReadFile("NOTICE")
	return string(b)
}
