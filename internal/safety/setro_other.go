//go:build !linux

package safety

import "errors"

func setRO(string) error { return errors.ErrUnsupported }
