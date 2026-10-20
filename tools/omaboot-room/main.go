// omaboot-room puts a stick's OMABOOT partition back after an image was written over it, the
// way boot-live does on its first save (send.RoomOn): a stick writer runs it as root after
// writing and reading back the image, to keep the stick's reports and settings. With --keep-only it only puts back one it finds; otherwise it makes a new one when
// there's none (the writer then copies its backup in).
//
//	omaboot-room [--keep-only] /dev/sdX
//
// It prints "kept /dev/sdX3" or "made /dev/sdX3", or "none" (--keep-only, nothing found).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/omaboot/boot-live/internal/send"
)

func main() {
	args, create := os.Args[1:], true
	if len(args) > 0 && args[0] == "--keep-only" {
		args, create = args[1:], false
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: omaboot-room [--keep-only] /dev/sdX")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	room, err := send.RoomOn(ctx, args[0], create)
	switch {
	case errors.Is(err, send.ErrNoneFound):
		fmt.Println("none")
	case err != nil:
		fmt.Fprintln(os.Stderr, "omaboot-room:", err)
		os.Exit(1)
	case room.Found:
		fmt.Printf("kept %s3 (%d MiB at sector %d)\n", args[0], room.Count>>11, room.Start)
	default:
		fmt.Printf("made %s3 (%d MiB at sector %d)\n", args[0], room.Count>>11, room.Start)
	}
}
