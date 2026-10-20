package bootwatch

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stream must be bootwatchd's byte for byte: the test vectors in its README.md (OpenSSL's
// `openssl enc -aes-128-ctr -K <seed> -iv 0` over zeros gives the same).
func TestStreamVectors(t *testing.T) {
	for _, c := range []struct {
		seed Seed
		want string // the first 32 bytes
	}{
		{Seed{}, "66e94bd4ef8a2c3b884cfa59ca342b2e58e2fccefa7e3061367f1d57a4e7455a"},
		{Seed{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, "c6a13b37878f5b826f4f8162a1c8d8797346139595c0b41e497bbde365f42d0a"},
	} {
		got := make([]byte, 32)
		Stream(c.seed).Read(got)
		if h := hex.EncodeToString(got); h != c.want {
			t.Errorf("seed %s: %s", c.seed, h)
		}
	}
	// reads of any size join up into the same stream
	want, joined, r := make([]byte, 64), []byte{}, Stream(Seed{7})
	Stream(Seed{7}).Read(want)
	for _, k := range []int{5, 11, 16, 32} {
		p := make([]byte, k)
		r.Read(p)
		joined = append(joined, p...)
	}
	if !bytes.Equal(joined, want) {
		t.Fatal("split reads differ from one read")
	}
}

func TestMatch(t *testing.T) {
	seed := NewSeed()
	data := make([]byte, 100_000)
	Stream(seed).Read(data)
	for _, c := range []struct {
		name      string
		body      []byte
		n, wantOK int64
	}{
		{"whole and right", data, 100_000, 100_000},
		{"short", data[:70_000], 70_000, 70_000},
		{"one wrong byte", flip(data, 70_000), 100_000, 70_000},
		{"first byte wrong", flip(data, 0), 100_000, 0},
		{"a sign-in page", []byte("<html>please sign in</html>"), 27, 0},
	} {
		n, ok, err := Match(bytes.NewReader(c.body), seed, nil)
		if err != nil || n != c.n || ok != c.wantOK {
			t.Errorf("%s: n %d ok %d err %v", c.name, n, ok, err)
		}
	}
}

func flip(b []byte, at int) []byte {
	c := bytes.Clone(b)
	c[at] ^= 0x40
	return c
}

// watchd answers the protocol as bootwatchd does (its own repo), with no limits.
func watchd(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	seed := func(k string) Seed {
		var s Seed
		hex.Decode(s[:], []byte(q.Get(k)))
		return s
	}
	switch r.URL.Path {
	case EchoPath:
		io.WriteString(w, q.Get("n"))
	case StreamPath:
		n, _ := strconv.ParseInt(q.Get("n"), 10, 64)
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		io.CopyN(w, Stream(seed("seed")), n)
	case SinkPath:
		if r.ContentLength < 0 {
			http.Error(w, "a Content-Length", http.StatusLengthRequired)
			return
		}
		n, ok, _ := Match(r.Body, seed("seed"), nil)
		json.NewEncoder(w).Encode(map[string]int64{"n": n, "match": ok})
	default:
		http.NotFound(w, r)
	}
}

func TestTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(watchd))
	defer srv.Close()
	var ends [3]int64
	c := &Client{URL: srv.URL, Progress: func(p Progress) { ends[p.Phase] = p.Done }}
	o := c.Test(context.Background())
	if !o.Verified() {
		t.Fatalf("not verified: %+v", o)
	}
	if o.Down != 64<<10+256<<10+speedPart || o.DownOK != o.Down || o.Up != UpSize || len(o.RTT) != echoes-1 || o.Family != "IPv4" {
		t.Fatalf("outcome %+v", o)
	}
	if ends != [3]int64{echoes, o.Down, UpSize} {
		t.Fatalf("progress ended at %v", ends)
	}
	if s := o.String(); !strings.HasPrefix(s, "boot.watch: 1.3 MB down verified (") || !strings.Contains(s, "64 KB up verified, round trip") || !strings.HasSuffix(s, ", IPv4") {
		t.Fatalf("evidence %q", s)
	}
}

// On a slow link (200 KB/s here) the 1 MB speed part is cut at 2 s; the test still passes on
// the parts before it, and the speed comes from the part that moved the most.
func TestSlowLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != StreamPath {
			watchd(w, r)
			return
		}
		var s Seed
		hex.Decode(s[:], []byte(r.URL.Query().Get("seed")))
		n, _ := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
		w.Header().Set("Content-Length", fmt.Sprint(n))
		src := Stream(s)
		for sent := int64(0); sent < n; sent += 10 << 10 {
			if _, err := io.CopyN(w, src, min(10<<10, n-sent)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	t0 := time.Now()
	o := (&Client{URL: srv.URL}).Test(context.Background())
	took := time.Since(t0)
	if !o.Verified() || o.Down <= 64<<10+256<<10 || o.Down >= 64<<10+256<<10+speedPart || took > 5*time.Second {
		t.Fatalf("outcome %s (%+v) after %s", o, o, took)
	}
	if o.Speed < 150<<10 || o.Speed > 250<<10 {
		t.Fatalf("speed %.0f B/s, want about 200 KB/s", o.Speed)
	}
}

// What a test meets on a bad network or a bad day, and how it sorts it.
func TestProblems(t *testing.T) {
	only := func(path string, h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == path {
				h(w, r)
				return
			}
			watchd(w, r)
		}
	}
	for _, c := range []struct {
		name    string
		h       http.HandlerFunc
		tls     bool
		want    Problem
		reached bool
	}{
		{"a portal redirects plain HTTP", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://portal.example.net/login", http.StatusFound)
		}, false, SignIn, false},
		{"a portal answers with its page", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<html>Welcome to Hotel Wi-Fi</html>")
		}, false, SignIn, false},
		{"someone else's certificate", watchd, true, SignIn, false},
		{"a wrong byte in the stream", only(StreamPath, func(w http.ResponseWriter, r *http.Request) {
			rec := httptest.NewRecorder()
			watchd(rec, r)
			b := rec.Body.Bytes()
			b[1000] ^= 1
			w.Write(b)
		}), false, Broken, true},
		{"the upload arrives wrong", only(SinkPath, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"n":65536,"match":4096}`)
		}), false, Broken, true},
		{"the stream stops short", only(StreamPath, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "0123456789")
		}), false, Broken, true},
		{"over the limit", only(StreamPath, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "busy", http.StatusTooManyRequests)
		}), false, Busy, true},
	} {
		srv := httptest.NewUnstartedServer(c.h)
		srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshake is expected
		if c.tls {
			srv.StartTLS() // its certificate isn't trusted: like a portal's
		} else {
			srv.Start()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		o := (&Client{URL: srv.URL}).Test(ctx)
		cancel()
		srv.Close()
		if o.Problem != c.want || o.Reached != c.reached || o.Verified() || o.Err == "" {
			t.Errorf("%s: problem %d reached %v err %q, want %d %v", c.name, o.Problem, o.Reached, o.Err, c.want, c.reached)
		}
	}
}
