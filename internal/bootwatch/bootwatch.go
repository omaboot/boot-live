// Package bootwatch is the Mac's side of the network checks' test protocol with bootwatchd
// (boot.watch, its own repo): an echo, a stream of bytes the Mac and the server both compute
// from a seed the Mac picks, and a sink that checks what the Mac sends. Every byte that moves
// is checked, so a sign-in page, a proxy's error page or a broken adapter can't pass for data.
// The protocol is public and tiny (bootwatchd's README.md, with its test vectors), so anyone can
// run their own server.
//
// The stream is AES-128-CTR with the seed as the key, a zero counter block and zero plaintext:
// standard, in every language, and the server stores nothing. Macs without AES-NI (Core 2)
// still compute it far faster than any link they have.
package bootwatch

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
)

// The protocol's requests.
const (
	EchoPath   = "/v1/echo"   // GET ?n=<32 hex>: the nonce back, as text
	StreamPath = "/v1/stream" // GET ?seed=<32 hex>&n=<bytes>: the seed's stream, its first n bytes
	SinkPath   = "/v1/sink"   // POST ?seed=<32 hex> + body: {"n": bytes read, "match": leading bytes that matched}
)

// Seed is a stream's key: 16 random bytes the Mac picks for one request.
type Seed [16]byte

// NewSeed is a fresh random seed (also a fresh echo nonce).
func NewSeed() Seed {
	var s Seed
	rand.Read(s[:]) // never fails (crypto/rand, Go 1.24+)
	return s
}

// String is the seed as the protocol writes it: 32 lowercase hex digits.
func (s Seed) String() string { return hex.EncodeToString(s[:]) }

// Stream is the seed's stream, endless: read as much of it as needed.
func Stream(s Seed) io.Reader {
	b, _ := aes.NewCipher(s[:]) // 16 bytes: AES-128, never an error
	return &stream{ctr: cipher.NewCTR(b, make([]byte, aes.BlockSize))}
}

type stream struct{ ctr cipher.Stream }

func (s *stream) Read(p []byte) (int, error) {
	clear(p)
	s.ctr.XORKeyStream(p, p)
	return len(p), nil
}

// Match reads r to its end and compares it with the seed's stream: n is every byte read, match
// the leading bytes that equal the stream (after the first wrong byte nothing more matches).
// each, when set, sees the count after every read.
func Match(r io.Reader, s Seed, each func(n int64)) (n, match int64, err error) {
	want := Stream(s)
	buf, exp := make([]byte, 32<<10), make([]byte, 32<<10)
	same := true
	for {
		k, rerr := r.Read(buf)
		if k > 0 {
			if same {
				want.Read(exp[:k])
				i := 0
				for i < k && buf[i] == exp[i] {
					i++
				}
				match += int64(i)
				same = i == k
			}
			n += int64(k)
			if each != nil {
				each(n)
			}
		}
		if rerr == io.EOF {
			return n, match, nil
		}
		if rerr != nil {
			return n, match, rerr
		}
	}
}
