//go:build !linux

package handson

import (
	"errors"
	"time"
)

func openInput(Device, bool) (Input, error)          { return nil, errors.ErrUnsupported }
func suspend(*System, time.Duration, SleepHow) Sleep { return Sleep{Err: errors.ErrUnsupported} }
func inhibitLid() func()                             { return func() {} }
