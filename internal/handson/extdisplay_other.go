//go:build !linux

package handson

import "errors"

func showCard(string, string) (Card, error) { return nil, errors.ErrUnsupported }
