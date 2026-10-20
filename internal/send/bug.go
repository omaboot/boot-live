package send

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// CanSendBug reports whether the image can send bug reports (PF-13): it has an upload key.
func (t Target) CanSendBug() bool { return t.Bugs != "" && t.BugKey != "" }

// BugRefused is a bug report the server turned away: Retry is when to try again (a limit), 0
// when it won't take it as it is.
type BugRefused struct {
	Message string
	Retry   time.Duration
}

func (e *BugRefused) Error() string { return e.Message }

// SendBug POSTs a bug report (a tar, scrubbed already) to the bug server's /v1/bugreport with
// the image's upload key, the Mac's hash (for the server's per-Mac limit, never kept) and
// OmaBoot?'s version. It returns the report's code (B-XXXX-XXXX). A *BugRefused is the server's
// answer; any other error means it wasn't reached (offline, or the server is down).
func SendBug(ctx context.Context, client *http.Client, t Target, mac, version string, body []byte) (string, error) {
	if !t.CanSendBug() {
		return "", errors.New("no bug report key in this image")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Bugs+"/v1/bugreport", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+t.BugKey)
	req.Header.Set("Content-Type", "application/x-tar")
	req.Header.Set("X-OmaBoot-Mac", mac)
	req.Header.Set("X-OmaBoot-Version", version)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch res.StatusCode {
	case http.StatusCreated:
		var a struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(b, &a); err != nil || a.Code == "" {
			return "", fmt.Errorf("the bug server's answer has no code: %s", bytes.TrimSpace(b))
		}
		return a.Code, nil
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		s, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		return "", &BugRefused{Message: "the bug server is busy", Retry: max(time.Duration(s)*time.Second, time.Minute)}
	case http.StatusBadGateway:
		return "", errors.New("the bug server couldn't keep it just now")
	}
	return "", &BugRefused{Message: string(bytes.TrimSpace(bytes.TrimPrefix(b, []byte("bootwatchd: "))))}
}
