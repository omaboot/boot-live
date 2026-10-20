//go:build !linux

package handson

import "errors"

// The optical drive: Linux only.
func (s *System) DriveStatus(string) (int, error) { return -1, errors.ErrUnsupported }

func (s *System) ReadDisc(string) DiscJob {
	return &fakeDisc{res: DiscRead{Skipped: "optical drives need Linux"}}
}

func (s *System) EjectDisc(string, bool) EjectJob {
	return doneEject{EjectResult{Err: "optical drives need Linux"}}
}

func (s *System) DiscBusy(string) string { return "" }
