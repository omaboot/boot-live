package app

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
)

// A bug report leaves the Mac scrubbed, file by file, with its manifest; the stick's copy is
// untouched.
func TestBugForSending(t *testing.T) {
	var in bytes.Buffer
	tw := tar.NewWriter(&in)
	kl := "wlan0: authenticate with 3c:22:fb:01:02:03\nDMI: Apple Inc. MacBookAir5,2, serial C02JK0ABDRVC\n"
	tw.WriteHeader(&tar.Header{Name: "bugreport-x/kernel-log.txt", Mode: 0o644, Size: int64(len(kl)), Typeflag: tar.TypeReg})
	tw.Write([]byte(kl))
	tw.Close()
	saved := in.String()
	out, err := bugForSending(in.Bytes(), bugManifest{Version: "0.9.3 dev", Identifier: "MacBookAir5,2", Saved: "bugreport-x.tar"})
	if err != nil {
		t.Fatal(err)
	}
	if in.String() != saved {
		t.Fatal("the stick's copy changed")
	}
	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(out))
	for h, err := tr.Next(); err == nil; h, err = tr.Next() {
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	k := got["bugreport-x/kernel-log.txt"]
	if strings.Contains(k, "3c:22:fb") || strings.Contains(k, "C02JK0ABDRVC") {
		t.Errorf("not scrubbed: %q", k)
	}
	if !strings.Contains(got["bugreport-x/manifest.json"], `"identifier": "MacBookAir5,2"`) {
		t.Errorf("manifest: %q", got["bugreport-x/manifest.json"])
	}
}
