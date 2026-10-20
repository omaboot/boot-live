package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// A copy saved before the report is sent is written again once it is: under its code's name,
// with the handle the report went with, and the copy saved first is gone.
func TestResaveAfterSend(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live, StickDir: dir, Version: "0.9.5 dev"})
	defer r.Close()
	if _, ok, _ := r.Resave(r.Report("", "", "")); ok {
		t.Fatal("resaved with no copy saved")
	}
	r.Record([]checks.Result{{ID: "storage.optical", Status: checks.Supported, Method: checks.Automatic, Evidence: "a disc read"}})
	first, err := r.Save(r.Report("", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	r.code = "0feeaafd1a" // sent
	p, ok, err := r.Resave(r.Report("CRH", "", ""))
	if !ok || err != nil {
		t.Fatalf("resave: ok %v, %v", ok, err)
	}
	if want := send.FileName(r.Ident.Identifier, "0feeaafd1a", time.Now()); p != want {
		t.Errorf("resaved as %s, want the code's name", p)
	}
	if _, err := os.Stat(filepath.Join(dir, first)); !os.IsNotExist(err) {
		t.Errorf("the copy saved first (%s) is still there", first)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, p)); !strings.Contains(string(b), `"CRH"`) {
		t.Error("the resaved copy hasn't the handle")
	}
}
