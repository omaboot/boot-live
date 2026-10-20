//go:build !linux

package console

import "errors"

func isConsole(int) bool          { return false }
func getCmap(int) ([]byte, error) { return nil, errors.ErrUnsupported }
func setCmap(int, []byte) error   { return errors.ErrUnsupported }
func blank(int, bool) error       { return errors.ErrUnsupported }
