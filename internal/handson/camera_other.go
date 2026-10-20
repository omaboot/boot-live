//go:build !(linux && amd64)

package handson

import "errors"

func openV4L2(string) (Cam, error) { return nil, errors.ErrUnsupported }
