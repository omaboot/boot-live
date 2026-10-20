#!/bin/bash
# Makes the file system images fsread_test.go reads: make-images.sh OUTDIR MKFS_HFSPLUS, then
# gzip -9n each of fat12 fat16 exfat mbr-fat32 gpt-exfat gpt-hfsplus hfsx hfs-wrap into here.
# Needs mkfs.fat, mkfs.exfat, sfdisk, udisksctl (loop devices and mounts without root) and
# hfsprogs' mkfs.hfsplus (its -w wants /usr/share/misc/hfsbootdata from the same package).
set -eu
cd "$1"
HFS=$2
python3 - <<'PY'
# block-numbered content: each 512-byte block names its own index, so a wrong offset shows
def content(n, tag):
    out = bytearray()
    i = 0
    while len(out) < n:
        line = (f"{tag} block {i:06d} " ).encode()
        blk = (line * (512 // len(line) + 1))[:511] + b"\n"
        out += blk
        i += 1
    return bytes(out[:n])
open("test.bin","wb").write(content(300*1024+123, "OMABOOT-TEST"))
open("fill.bin","wb").write(content(100*1024, "FILL"))
PY
fill() { # mp: fill the volume with 48 KiB files, free every other one, then copy the test
  # file in: it has to go in the holes, so it ends up in pieces on every file system
  local mp=$1 i=0
  head -c 49152 /dev/zero | tr '\0' F > f48.bin
  mkdir "$mp/fill" # not the top folder: FAT12 and FAT16 have room for only so many entries there
  while cp f48.bin "$mp/fill/f$i.bin" 2>/dev/null; do i=$((i+1)); done
  rm -f "$mp/fill/f$i.bin"; sync
  for ((k=0; k<i; k+=2)); do rm "$mp/fill/f$k.bin"; done; sync
  cp test.bin "$mp/OMABOOT-TEST.BIN"; mkdir -p "$mp/sub"; cp f48.bin "$mp/sub/OMABOOT-TEST.BIN"; sync
}
mountimg() { # img label -> mp; loopdev in LOOP
  LOOP=$(udisksctl loop-setup -f "$1" --no-user-interaction | sed -n 's/.* as \(\/dev\/loop[0-9]*\)\.$/\1/p')
  sleep 1
  MP=$(findmnt -n -o TARGET "$LOOP" || true)
  if [ -z "$MP" ]; then MP=$(udisksctl mount -b "$LOOP" --no-user-interaction | sed -n 's/.* at \(.*\)$/\1/p'); fi
}
umountimg() { udisksctl unmount -b "$LOOP" --no-user-interaction >/dev/null; udisksctl loop-delete -b "$LOOP" --no-user-interaction; }
make1() { # name size mkfs-cmd...
  local name=$1 size=$2; shift 2
  truncate -s "$size" "$name"
  "$@" "$name" >/dev/null
  mountimg "$name"
  fill "$MP"
  umountimg
}
make1 fat12.img 2M mkfs.fat -F 12 -n FAT12T
make1 fat16.img 4M mkfs.fat -F 16 -s 1 -n FAT16T
make1 fat32.img 34M mkfs.fat -F 32 -s 1 -n FAT32T
make1 exfat.img 4M mkfs.exfat -L EXFATT
make1 hfsplus.img 4M "$HFS" -v HFSPT
make1 hfs-wrap.img 4M "$HFS" -w -b 512 -v HFSPW
make1 hfsx.img 4M "$HFS" -s -v HFSXT
# partitioned: MBR with FAT32 at 1 MiB, GPT with exFAT at 1 MiB, GPT with HFS+ at 1 MiB
compose() { # out table fsimg type
  local out=$1 table=$2 fs=$3 type=$4
  local sz=$(( $(stat -c %s "$fs") + 2*1024*1024 ))
  truncate -s $sz "$out"
  printf 'label: %s\nstart=2048, size=%d, type=%s\n' "$table" $(( $(stat -c %s "$fs") / 512 )) "$type" | sfdisk -q "$out"
  dd if="$fs" of="$out" bs=1M seek=1 conv=notrunc status=none
}
compose mbr-fat32.img dos fat32.img c
compose gpt-exfat.img gpt exfat.img EBD0A0A2-B9E5-4433-87C0-68B6B72699C7
compose gpt-hfsplus.img gpt hfsplus.img 48465300-0000-11AA-AA11-00306543ECAC
rm fill.bin f48.bin
