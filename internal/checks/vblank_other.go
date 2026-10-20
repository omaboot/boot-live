//go:build !linux

package checks

import "context"

func countVblanks(_ context.Context, card, conn string) scanout {
	return scanout{Card: card, Conn: conn, Skip: "DRM isn't available on this system"}
}
