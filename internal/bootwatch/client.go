package bootwatch

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// What the test moves. Supported needs MinDown verified down and UpSize verified up; the
// speed part only measures the link, within speedCap.
const (
	MinDown   = 256 << 10
	UpSize    = 64 << 10
	speedPart = 1 << 20
	speedCap  = 2 * time.Second
	echoes    = 6 // the first opens the connection (TLS included); the rest are timed
)

// downParts are fetched in order and must all arrive whole and right; the speed part follows.
var downParts = [...]int64{64 << 10, 256 << 10}

// Client runs the test against one bootwatchd over its own connection.
type Client struct {
	URL string // the server: https://boot.watch, or http://host:port for a dev server
	// Control runs on each socket before it connects: network.Watch binds it to the interface
	// under test (SO_BINDTODEVICE). nil: the run's own routes decide.
	Control func(network, address string, c syscall.RawConn) error
	// Progress, when set, follows the bytes as they move (the hands-on tests' animation).
	Progress func(Progress)
}

// Phase is what the test is doing.
type Phase int

const (
	Reaching  Phase = iota // the echoes: Done of Total echoes
	Receiving              // the stream: Done of Total bytes
	Sending                // the sink: Done of Total bytes
)

// Progress is how far a phase got.
type Progress struct {
	Phase       Phase
	Done, Total int64
}

// Problem is why a test stopped short.
type Problem int

const (
	OK     Problem = iota
	SignIn         // the network wants a sign-in page: something else answered for boot.watch
	Clock          // the Mac's clock is off, so boot.watch's certificate isn't valid for it
	Busy           // boot.watch turned the test away (a limit): not the Mac's doing
	Broken         // no answer, a wrong answer, or wrong or missing bytes
)

// Outcome is what a test found: the report's evidence, and what the verdict is made of.
type Outcome struct {
	Reached bool            // boot.watch answered an echo over this connection
	Family  string          // "IPv4" or "IPv6": what the connection went over
	RTT     []time.Duration // the timed echoes' round trips
	Down    int64           // bytes received from the stream
	DownOK  int64           // of those, the ones that matched it
	Speed   float64         // bytes a second, over the largest part received
	Up      int64           // bytes sent to the sink
	UpOK    int64           // of those, the ones boot.watch says matched
	Problem Problem
	Err     string // what went wrong, when Problem isn't OK
}

// Verified reports whether data moved both ways and every byte was right.
func (o Outcome) Verified() bool { return o.Problem == OK && o.DownOK >= MinDown && o.UpOK >= UpSize }

// Moved is the payload that crossed the connection both ways.
func (o Outcome) Moved() int64 { return o.Down + o.Up }

// String is the outcome for a report: "boot.watch: 1.3 MB down verified (6.8 MB/s), 64 KB up
// verified, round trip 19 ms median (17–24 ms), IPv4".
func (o Outcome) String() string {
	var p []string
	if o.Down > 0 {
		s := size(o.Down) + " down"
		switch {
		case o.DownOK == o.Down:
			s += " verified"
		default:
			s += ", " + size(o.DownOK) + " of it right"
		}
		if o.Speed > 0 {
			s += " (" + rate(o.Speed) + ")"
		}
		p = append(p, s)
	}
	if o.Up > 0 {
		s := size(o.Up) + " up"
		if o.UpOK == o.Up {
			s += " verified"
		} else {
			s += ", " + size(o.UpOK) + " of it right"
		}
		p = append(p, s)
	}
	if len(o.RTT) > 0 {
		d := slices.Clone(o.RTT)
		slices.Sort(d)
		p = append(p, fmt.Sprintf("round trip %d ms median (%d–%d ms)", ms(d[len(d)/2]), ms(d[0]), ms(d[len(d)-1])))
	} else if o.Reached {
		p = append(p, "echo answered")
	}
	if o.Family != "" {
		p = append(p, o.Family)
	}
	if len(p) == 0 {
		return "boot.watch: nothing moved"
	}
	return "boot.watch: " + strings.Join(p, ", ")
}

func ms(d time.Duration) int64 { return d.Round(time.Millisecond).Milliseconds() }

func size(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

func rate(b float64) string {
	if b >= 1<<20 {
		return fmt.Sprintf("%.1f MB/s", b/(1<<20))
	}
	return fmt.Sprintf("%d KB/s", int64(b)>>10)
}

// Test runs the whole test on one connection: the echoes, the stream (64 KB, 256 KB), the sink
// (64 KB), then the speed part (1 MB, cut at 2 s). The context caps it all (the checks give it
// 15 s).
func (c *Client) Test(ctx context.Context) (o Outcome) {
	s := c.open()
	defer s.tr.CloseIdleConnections()
	defer func() { o.Family = s.family() }()
	if !s.reach(ctx, &o, echoes) {
		return o
	}
	total := downParts[0] + downParts[1] + speedPart
	var best part
	for _, n := range downParts {
		pt, ok := s.down(ctx, &o, n, total, false)
		if !ok {
			return o
		}
		best = best.max(pt)
	}
	if !s.up(ctx, &o) {
		return o
	}
	sctx, cancel := context.WithTimeout(ctx, speedCap)
	pt, _ := s.down(sctx, &o, speedPart, total, true)
	cancel()
	o.Speed = best.max(pt).rate()
	return o
}

// Reach asks for one echo, after a plain-HTTP echo when the server is HTTPS (where a sign-in
// page shows itself): the Network step's check, and whether boot.watch answers over the run's
// own connection when a test over another failed.
func (c *Client) Reach(ctx context.Context) Outcome {
	s := c.open()
	defer s.tr.CloseIdleConnections()
	var o Outcome
	s.reach(ctx, &o, 1)
	o.Family = s.family()
	return o
}

// session is one test's connection and what it learned about it.
type session struct {
	c  *Client
	tr *http.Transport
	hc *http.Client
	mu sync.Mutex
	to net.Addr // the last connection's far end
}

func (c *Client) open() *session {
	s := &session{c: c}
	d := &net.Dialer{Timeout: 10 * time.Second, Control: c.Control}
	s.tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := d.DialContext(ctx, network, addr)
			if err == nil {
				s.mu.Lock()
				s.to = conn.RemoteAddr()
				s.mu.Unlock()
			}
			return conn, err
		},
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  true, // the bytes as they are: data is the point
		MaxConnsPerHost:     1,    // one connection, reused
		IdleConnTimeout:     30 * time.Second,
	}
	s.hc = &http.Client{Transport: s.tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s
}

func (s *session) family() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.to.(*net.TCPAddr); ok {
		if a.IP.To4() != nil {
			return "IPv4"
		}
		return "IPv6"
	}
	return ""
}

func (s *session) progress(ph Phase, done, total int64) {
	if s.c.Progress != nil {
		s.c.Progress(Progress{Phase: ph, Done: done, Total: total})
	}
}

func (s *session) get(ctx context.Context, method, u string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "boot-live")
	if b, ok := body.(*sending); ok { // the sink wants a Content-Length, not a chunked body
		req.ContentLength = int64(b.r.Len())
	}
	return s.hc.Do(req)
}

func (s *session) at(path, query string) string {
	return strings.TrimRight(s.c.URL, "/") + path + "?" + query
}

// reach runs the plain-HTTP echo (an HTTPS server only), then count echoes over the server's
// own scheme; the ones after the first are timed.
func (s *session) reach(ctx context.Context, o *Outcome, count int) bool {
	if why := s.plainEcho(ctx); why != "" {
		o.Problem, o.Err = SignIn, why
		return false
	}
	for i := range count {
		n := NewSeed()
		t0 := time.Now()
		res, err := s.get(ctx, http.MethodGet, s.at(EchoPath, "n="+n.String()), nil)
		if err != nil {
			o.Problem, o.Err = classify(err)
			return false
		}
		got, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		res.Body.Close()
		rtt := time.Since(t0)
		plain := strings.HasPrefix(s.c.URL, "http:")
		switch {
		case res.StatusCode == http.StatusTooManyRequests:
			o.Problem, o.Err = Busy, "boot.watch is busy (too many tests from this network just now)"
			return false
		case plain && signInAnswer(res, ""):
			o.Problem, o.Err = SignIn, "the echo came back as "+answered(res)+": a sign-in page"
			return false
		case res.StatusCode != http.StatusOK:
			o.Problem, o.Err = Broken, "the echo came back as "+answered(res)
			return false
		case string(got) != n.String():
			o.Problem, o.Err = Broken, "the echo came back wrong"
			if plain {
				o.Problem, o.Err = SignIn, "the echo came back as something else: a sign-in page"
			}
			return false
		}
		o.Reached = true
		if i > 0 {
			o.RTT = append(o.RTT, rtt)
		}
		s.progress(Reaching, int64(i+1), int64(count))
	}
	return true
}

// plainEcho asks an HTTPS server for an echo over plain HTTP, where a captive portal answers
// with its sign-in page or a redirect to it (over HTTPS it can only break the connection). It
// says why it looks like a sign-in page, or "" when it doesn't, or can't tell (no answer at
// all; a redirect to the same server's HTTPS address, which only enforces HTTPS).
func (s *session) plainEcho(ctx context.Context) string {
	u, err := url.Parse(s.c.URL)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	host := u.Hostname()
	u.Scheme, u.Host = "http", host
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n := NewSeed()
	res, err := s.get(pctx, http.MethodGet, strings.TrimRight(u.String(), "/")+EchoPath+"?n="+n.String(), nil)
	if err != nil {
		return ""
	}
	got, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	res.Body.Close()
	switch {
	case signInAnswer(res, host):
		return "boot.watch over plain HTTP came back as " + answered(res) + ": a sign-in page"
	case res.StatusCode == http.StatusOK && string(got) != n.String():
		return "boot.watch over plain HTTP came back as another page: a sign-in page"
	}
	return ""
}

// signInAnswer: a redirect elsewhere (not to host's HTTPS address), or 511 Network
// Authentication Required.
func signInAnswer(res *http.Response, host string) bool {
	if res.StatusCode == http.StatusNetworkAuthenticationRequired {
		return true
	}
	if res.StatusCode < 300 || res.StatusCode > 399 {
		return false
	}
	to, err := res.Location()
	return err != nil || host == "" || to.Scheme != "https" || to.Hostname() != host
}

// answered says what came back instead: "HTTP 302 to portal.example.net", "HTTP 503".
func answered(res *http.Response) string {
	s := fmt.Sprintf("HTTP %d", res.StatusCode)
	if to, err := res.Location(); err == nil {
		s += " to " + to.Hostname()
	}
	return s
}

// classify sorts a request's error: a certificate for another name or from an unknown
// authority is someone else answering for boot.watch (a portal's DNS, a proxy); one outside its
// dates is the Mac's clock (a flat PRAM battery); anything else is Broken.
func classify(err error) (Problem, string) {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // the reason, without the URL
	}
	var he x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &he), errors.As(err, &ua):
		return SignIn, "someone else answered for boot.watch (" + err.Error() + "): a sign-in page or a proxy"
	case errors.As(err, &ci) && ci.Reason == x509.Expired:
		return Clock, "the Mac's clock is off (" + time.Now().UTC().Format(time.DateOnly) + "), so boot.watch's certificate can't be checked"
	}
	return Broken, err.Error()
}

// part is one stream part's bytes and the time they took from the first byte.
type part struct {
	n   int64
	dur time.Duration
}

func (p part) max(q part) part {
	if q.n > p.n {
		return q
	}
	return p
}

func (p part) rate() float64 {
	if p.dur <= 0 {
		return 0
	}
	return float64(p.n) / p.dur.Seconds()
}

// down fetches n bytes of a fresh seed's stream and checks every one. The speed part may be
// cut by its own deadline: what arrived counts, right bytes only.
func (s *session) down(ctx context.Context, o *Outcome, n, total int64, speed bool) (part, bool) {
	seed := NewSeed()
	res, err := s.get(ctx, http.MethodGet, s.at(StreamPath, fmt.Sprintf("seed=%s&n=%d", seed, n)), nil)
	if err != nil {
		if speed && ctx.Err() != nil {
			return part{}, true
		}
		o.Problem, o.Err = classify(err)
		return part{}, false
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		o.Problem, o.Err = Busy, "boot.watch is busy (too many tests from this network just now)"
		return part{}, false
	case res.StatusCode != http.StatusOK:
		o.Problem, o.Err = Broken, fmt.Sprintf("the %s part came back as %s", size(n), answered(res))
		return part{}, false
	}
	t0, base := time.Now(), o.Down
	got, match, err := Match(io.LimitReader(res.Body, n+1), seed, func(k int64) { s.progress(Receiving, base+k, total) })
	pt := part{n: match, dur: time.Since(t0)}
	o.Down += got
	o.DownOK += match
	switch {
	case match < got:
		o.Problem, o.Err = Broken, fmt.Sprintf("byte %d of the %s part was wrong", match, size(n))
		return pt, false
	case err != nil && speed && ctx.Err() != nil:
		return pt, true // cut at speedCap: as far as it got
	case err != nil:
		o.Problem, o.Err = classify(err)
		o.Err = fmt.Sprintf("the %s part broke off after %d bytes: %s", size(n), got, o.Err)
		return pt, false
	case got != n:
		o.Problem, o.Err = Broken, fmt.Sprintf("the %s part came with %d bytes", size(n), got)
		return pt, false
	}
	return pt, true
}

// up sends UpSize bytes of a fresh seed's stream to the sink, which says how many matched.
func (s *session) up(ctx context.Context, o *Outcome) bool {
	seed := NewSeed()
	body := make([]byte, UpSize)
	Stream(seed).Read(body)
	res, err := s.get(ctx, http.MethodPost, s.at(SinkPath, "seed="+seed.String()), &sending{r: bytes.NewReader(body), s: s})
	if err != nil {
		o.Problem, o.Err = classify(err)
		return false
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		o.Problem, o.Err = Busy, "boot.watch is busy (too many tests from this network just now)"
		return false
	case res.StatusCode != http.StatusOK:
		o.Problem, o.Err = Broken, "the upload came back as "+answered(res)
		return false
	}
	var a struct{ N, Match int64 } // {"n": bytes read, "match": leading bytes that matched}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&a); err != nil {
		o.Problem, o.Err = Broken, "the upload's answer wasn't boot.watch's"
		return false
	}
	o.Up, o.UpOK = a.N, a.Match
	if a.N != UpSize || a.Match != UpSize {
		o.Problem, o.Err = Broken, fmt.Sprintf("boot.watch got %d of %d bytes up, %d of them right", a.N, UpSize, a.Match)
		return false
	}
	return true
}

// sending is the upload's body, reporting progress as it's read. Its length stays known (Len),
// so the request carries a Content-Length.
type sending struct {
	r    *bytes.Reader
	s    *session
	done int64
}

func (b *sending) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.done += int64(n)
	b.s.progress(Sending, b.done, UpSize)
	return n, err
}
