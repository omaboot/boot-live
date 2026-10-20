# boot-live

The tester inside **OmaBoot? Live**: a bootable image that tests an Intel Mac (2006–2020) for
[Omarchy](https://omarchy.org) without touching its disks, and sends the results to
[DoesItOmarchy.com](https://doesitomarchy.com). It runs on the Mac's bare Linux console
(16 colours, one bitmap font), guided by Boot, a small character who explains what's happening.

**Status: S1, 0.3.1 dev (prototype v24 and the owner's 0.3.0 review).** The real run works end to end: it makes the internal
disks read-only, identifies the Mac against the catalog snapshot, gets online (Ethernet, USB
tethering, Wi-Fi through iwd, or a static address), runs the quick pass, shows Quick results, and
on Finish submits the run's one report or saves a copy to external storage. `--auto` does all
of that without input, for the image build's QEMU test. `--demo` drives the same six steps
(Hello · Which Mac · Network · Quick pass · Quick results · Finish) with a scripted backend on a
MacBook Air (13-inch, Mid 2012) [MacBookAir5,2] whose results are made up; you pick the network,
type a password (any will do), and so on. 0.2.0 ran on a MacBookAir5,2 and an iMac10,1.

## Try it in a terminal

Needs Go 1.27 and a terminal of at least 120×40 characters (160×50 matches a 13" MacBook).

```sh
make demo                   # builds dist/boot-live, runs it with --demo --term
./dist/boot-live --demo --term --plain           # without Boot
./dist/boot-live --demo --term --edition omarchy # the endings of OmaBoot? Omarchy
```

Keys: what you do on a screen is the ▶ list under its text: ↑↓ choose (1–9 jump), Enter go; Tab
moves between fields, and on Network between the Wi-Fi list and the actions under it. The hint
bar under the frame holds the same three secondary keys on every screen, highlighted where they
work: **Esc** quit (it always asks first, in a red box beside Boot whose words say what quitting
would lose and what follows on the image; the quick pass pauses while it asks; "close" on the
read-only report and the full log),
**delete** (Backspace) back a step (Which Mac, Network, Quick results, Finish until the report is
sent; in a text field it deletes), **L** the full log (quick pass, Quick results, Finish, report).
Ctrl+C quits at once. Keyboard only.

On a hands-on test each answer shows its key: 1, 2, 3 … pick the answer showing that number,
s skips; ↑↓ and Enter work too. A decided test holds its result: 1 Continue, 2 Redo (a new
result replaces it), and delete redoes it as well. delete on a test that hasn't decided opens
the one before it, paused: nothing starts, its last result shows, Continue or Redo.

**The console** (LIVE-PLAN §24): **`** (the key left of 1; § on ISO keyboards) opens a command
console in its own box above the hint bar, on every screen; Ctrl+` (NUL) opens it from a text
field too. The frame shrinks to make room and the screen keeps running. Esc closes it, Tab takes
the dim completion, ↑↓ go through the history, Ctrl+↑/↓ make it tall (half the screen) or short (on the console
the real run loads two keymap lines for them with loadkeys, saving the keymap with dumpkeys first
and loading it back on exit; the stock keymap sends plain arrows), PgUp/PgDn scroll,
`?` lists the commands. A hands-on test that has the keyboard keeps it (the key dims). Read-only:
`why <test>`, `log [filter]`, `ids`, `catalog source|mac|search <text>|fixes|ports` (the catalog
snapshot in use, offline), `report [json]`, `note <text>` (into the report's notes,
which are public), `bugreport` (the full log, the kernel log, the report and the screen as a tar
in results/ on external storage only: it may contain serial numbers), `boot say <text>` (in
Boot's own box on the screen, up to 60 characters, for 8 s or until his own line changes),
`clear`, `close`, and `quit` (or `exit`), `restart`, `shutdown`: Esc's quit question with that
choice selected. Redo: `rerun
<test>` measures a quick-pass check again (a hands-on test opens again); the new measurement
decides and the first run goes into its evidence. Dev builds add `force <test> <result>`, which
marks the run: the frame's top edge says so, Submit opens an amber box, and DoesItOmarchy.com
never takes the report (staging and external storage still do; `send.Allowed` refuses it last); and
`goto`, `font 16|24|32`, `fps`, `target`. Every command and its output go to the full log, and
the report lists the redo and change commands (`console`). `--auto` runs a few read-only commands
after the quick pass.

Screensaver: on a screen waiting for input, Boot dozes after 5 minutes (one frame a second,
counting down the minutes to his REM sleep) and the console is blanked after 15 (TIOCLINUX); the
key that wakes it does nothing else. Never during the quick pass.

## The real run

Without `--demo`, boot-live tests the Mac it runs on. **It starts only on the OmaBoot? live
system**: an archiso boot (`/run/archiso`, or `archisobasedir=` on the kernel command line) that
carries `/usr/share/omaboot/`. Anywhere else it refuses (exit 3) before touching anything,
because the first thing it does is make the internal disks read-only.

1. **Safety** (`internal/safety`). Every internal disk, its partitions and any device-mapper/md
   device on them is set read-only (`BLKROSET`, what `blockdev --setro` does) and read back from
   sysfs; if any stays writable, or is mounted read-write, the run stops on Hello. "Internal" is
   decided by bus, never by the removable flag: USB and SD stay writable, SATA, NVMe and anything
   unrecognised (Thunderbolt, FireWire) become read-only. The OmaBoot? drive is on USB. A thermal
   watchdog reads the CPU and GPU sensors every second and stops any load 5 °C below a sensor's
   `temp*_crit`. Nothing writes to NVRAM, EFI variables, the SMC or firmware.
2. **Which Mac** (`internal/identify`). DMI (`product_name`, `board_name` = Apple's board ID,
   chassis), the CPU, memory, PCI functions with subsystem IDs and drivers, and USB devices
   (sticks left out) become a `match.Probe` for doesitomarchy's `pkg/match`, run on the catalog
   snapshot built into the binary (`assets/`, `make snapshot`). One exact match is shown; several
   give a pick list (plus "Not sure"); none gives the "not in the catalog yet" path, where the
   report can still be saved. Serial numbers, MAC addresses and host names are never read.
3. **Network** (`internal/network`). Ethernet and USB tethering come up by themselves
   (systemd-networkd); online means a default route and the target's name resolving. The
   Ethernet and tethering lines follow the links live while the step is on screen. Otherwise
   a Wi-Fi list from iwd over D-Bus, a masked password (kept in memory, handed to iwd through
   its agent), connect, wait for DHCP. **Set static IP address** (address/prefix, gateway, DNS,
   checked before use) writes `/run/systemd/network/10-omaboot-static-<iface>.network` and runs
   `networkctl reload` and `networkctl reconfigure <iface>`, on the wired link with a cable in,
   else the joined Wi-Fi, else the first wired link; /run keeps it for this run only. "Skip, test
   offline" is always there. Going back to the step keeps the connection. Once online the catalog
   snapshot is refreshed from the target (`If-None-Match`), and boot.watch is asked for one echo
   (plain HTTP first): a sign-in page answering for it shows "this network wants a sign-in page
   first". The echo never decides whether the Mac is online.
4. **Quick pass** (`internal/checks`): `boot.installer-efi64`, `boot.install` (live-limit),
   `display.native-resolution`, `graphics.integrated`/`graphics.discrete` (the GPU helper),
   `graphics.power-down` (two GPUs), `power.battery-status`, the audio codec (an extra),
   `network.wifi`, `network.ethernet`, `thermal.sensors` under a CPU load, and `thermal.fans`,
   `input.keyboard-backlight`, `input.ambient-light-sensor`, which a live boot decides only on
   T2 Macs or with the OmaBoot? applesmc fix. Each check's pass/partial/fail rule is written
   next to it against the criterion's wording. Wi-Fi and Ethernet only report what they see here
   (never a pass): their hands-on tests (`internal/handson/net_wifi.go`, `net_ethernet.go`,
   offered whatever the Network step did) join a network or wait for a cable, keep the run online
   through it, and move their data through
   bootwatchd (`internal/bootwatch`, the `bootwatchd` repo): on a socket bound to the interface,
   64 KB and 256 KB down and 64 KB up, every byte checked against a stream both sides compute
   from a seed the Mac picks, then a 1 MB part cut at 2 s for the speed. Every other criterion
   that applies goes in the report as not_tested: `live-limit` where a live boot can't decide
   it, else `not-in-profile`.
   Fixes count only when they took effect: the radeon panel fix by its kernel log line, the
   applesmc fix when applesmc loaded from `updates/` and registered `smc::kbd_backlight`.
   The band shows each check's effect from when it really starts until its result's line shows
   (the runner reports start times as well as finish times; while a check runs, effects queued
   ahead of it shorten to 1.5 s). Before the first graphics check the screen shows a notice ("Dark screen ahead?", 15 s
   countdown, Enter starts at once); the check is held until the notice ends and the paced
   playback has caught up, so it really runs after the warning. `--auto` has no notice.
5. **Report** (`internal/send`). One report per run, sent or saved only on Finish. A schema v1 `report.File` (`context: live`, identifier,
   configuration, hardware probe, items, extras, fixes, Omarchy version and image, kernel,
   `source: boot-live`, the name to credit and the consent notice shown), checked with
   `report.Check` before it goes anywhere. **Submit report** POSTs it to `<target>/api/v1/reports`
   with `Authorization: Bearer <key>`; the answer's code and page are shown, a 4xx shows the
   server's problems, a 429 says when to try again; once a code came back, Submit is greyed out
   ("Submitted · <code>") and a second send is refused. **Save a copy to external storage**
   writes `results/<identifier>-<code or time>.json` on the drive labelled `BOOTLIVE`, else on the
   OmaBoot? drive's own `OMABOOT` reports partition (mounted if needed, never formatted, synced,
   unmounted again). On screen, saving says "external storage" and the drive OmaBoot? runs from is
   "the OmaBoot? drive"; "stick" is left for USB devices in general.

The GPU check runs `/usr/lib/omaboot/egl-probe` (`helpers/egl-probe/`, C): for each render node
it opens GBM, makes an OpenGL ES context (3.x, else 2.0) with no window system, renders one
frame offscreen, reads a pixel back and times a 1080p compositor-like load, printing `key=value`
lines. Without the helper the GPU criteria are not_tested (reason other) and say so.

### Test media

Any disc, stick, card or drive does for the storage tests. Two downloads at
`get.omaboot.live/test/` (made once, not per release; their `SHA256SUMS` is signed with the
release keys) also prove the bytes came back right, not just that bytes came back:

- `omaboot-test.iso`, the **OMABOOT_TEST disc**, for the Optical drive test: burn it to a CD-R
  (`go run ./tools/testcd DIR`, then `xorriso -as mkisofs -V OMABOOT_TEST -iso-level 3`). The
  test reads its first MB and checks `OMABOOT-TEST.BIN`'s pattern (`internal/handson/testdisc.go`).
- `OMABOOT-TEST.BIN`, the **test file**, for the USB, SD card and FireWire tests: copied to the
  top folder of any stick, card or drive, nothing erased (`go run ./tools/testfile DIR`; 4 KB of
  header, then 32 MiB of the same pattern; `internal/handson/testfile.go`). The tests find it
  without mounting anything: `internal/fsread` reads the partition table (MBR, GPT, Apple's) and
  the file system's own structures (FAT12/16/32, exFAT, HFS+/HFSX) straight off the device, then
  every byte of the file is checked. A USB disk without it is read twice and compared, as before;
  an SD card or FireWire drive without it is only detected.

### target.conf

Where reports go is set when the image is built, in `/usr/share/omaboot/target.conf`:

```
# a dev image: reports go to a staging site
url=http://localhost:8080
key=doi_…
```

`url=` is the DoesItOmarchy site, `key=` the boot-live source key for it; `#` starts a comment.
`watch=` is the bootwatchd the Wi-Fi and Ethernet checks test through (default
`https://boot.watch`; a dev image can name its own, e.g. `watch=http://localhost:8470`). `--target`, `--key` and
`--watch` override them. Without a key, Submit is greyed out with a note and Save a copy to external
storage still works. The image may also write `/usr/share/omaboot/omarchy-version` (e.g.
`4.0.4`) and `/usr/share/omaboot/image` (e.g. `omaboot-live dev 0b8738f`); without them the
report takes the Omarchy version from `/etc/os-release` or the package database (else the
built-in `OMARCHY`), and the image name from `/usr/share/omaboot/build`.

### --auto

No input at all, for the QEMU smoke test: identify (the exact match, else the best one), use
whatever network comes up by itself within 20 s, run the quick pass, then submit if there's a
target, a key and a network (and save a copy to external storage), else save to external storage. A
failed submit falls back to saving. Progress lines (`boot-live: …`) go to stderr and the serial
port; the run ends with exactly one line on stdout and on `/dev/ttyS0` (when it exists):

```
OMABOOT-AUTO: submitted <code>
OMABOOT-AUTO: saved results/<identifier>-<time>.json
OMABOOT-AUTO: failed <reason>
```

and exits 0 (submitted or saved) or 1 (failed); 3 when it isn't the live system.

### Developing without a Mac: --sysroot

`--sysroot testdata/macbookair5,2` reads every `/sys`, `/proc`, `/dev` and `/etc` file under
that tree instead of the machine's, and **writes nothing**: the disk step is a dry run, Wi-Fi is
simulated from the tree's `iwd-networks.txt`, a static address is only pretended, the GPU
helper's output comes from its `egl-probe.txt`, saving needs `--stick DIR`, and Shutdown and
Restart only say so. The
trees in `testdata/` are built by `testdata/gen-fixtures.py` from the three test Macs' real probe
data (omaboot-spikes, omacdiag-runs): MacBookAir5,2 (applesmc fix), iMac10,1 (radeon fix),
iMac18,3, and a QEMU guest that isn't a Mac.

```sh
./dist/boot-live --sysroot testdata/macbookair5,2 --term --stick /tmp/stick
./dist/boot-live --sysroot testdata/imac10,1 --auto --stick /tmp/stick --target http://localhost:8080 --key doi_…
```

## Flags

| Flag | |
|---|---|
| `--demo` | The S1 screens with the scripted demo backend. |
| `--auto` | The real run with no input (see above). |
| `--sysroot DIR` | Read the system's files under DIR (a fixture tree); writes nothing. |
| `--target URL`, `--key KEY` | Where reports go and the source key, over `target.conf`. |
| `--watch URL` | The bootwatchd the network checks test through, over `target.conf` (default `https://boot.watch`). |
| `--stick DIR` | Save reports under DIR instead of on external storage. |
| `--helper PATH` | The GPU helper (default `/usr/lib/omaboot/egl-probe`; a BOOTLIVE drive can bring its own). |
| `--load D` | How long the thermal check loads the CPU (default 20s; 2s with `--sysroot`). |
| `--plain` | Boot layer off: no Boot, no band effects, no Boot lines. The screens still work, with a short headline area instead of Boot's. |
| `--edition live\|omarchy` | The image it runs in: OmaBoot? Live (the stand-alone tester) or OmaBoot? Omarchy (the Omarchy installer with the tester). Changes the product name, the version line and the endings: only OmaBoot? Omarchy offers "Install Omarchy now". |
| `--term` | Development in a terminal emulator: no console font or palette setup; colours are our palette's RGB values. Used automatically when the screen isn't a Linux console. |
| `--version` | Print the version. |

Exit status: 0 done (or quit), 1 failed, 2 bad flags, 3 refused (not the live system, or the
disks couldn't be made read-only), 10 the person chose "Install Omarchy now" or "Go to the
installer" (OmaBoot? Omarchy: the image's script starts the installer). "Shutdown" and "Restart"
run `systemctl poweroff` / `reboot` themselves once the console is restored.

## On the Linux console

On a virtual console (`TERM=linux`) the tool:

- sets our 16-colour palette with `\e]P` and puts the previous one back on exit (read and
  written with the console's `GIO_CMAP`/`PIO_CMAP`; `\e]R` if that fails);
- picks omaboot-v at 16, 24 or 32 px from the screen's physical size (the panel's EDID size and
  the framebuffer resolution: about 3.6 mm per text line on laptops, 5.5 mm on desktops), loads
  it once with `setfont` before the first screen, and restores the original font on exit;
- hides the cursor and redraws only the lines that changed (Bubble Tea's renderer compares each
  frame with the last line by line), with animations kept inside Boot's area at 15 fps.

Fonts are looked up in `/usr/share/omaboot/fonts/` (the images), then `assets/fonts/` (this repo).

## Build

```sh
make build    # static binary (CGO_ENABLED=0) with version and commit: dist/boot-live
make helper   # the GPU helper against this machine's Mesa: dist/egl-probe (needs Mesa's headers)
make snapshot # refresh assets/snapshot.json and macs.json (SNAPSHOT_FROM=, default https://doesitomarchy.com)
make check    # go vet, tests (both with and without the dev tag), catalogs up to date, gofmt, scrub check
make screens OUT=/tmp/screens   # every demo screen as .txt, .ans and .png at 160×45, 160×50, 120×40
make screens OUT=/tmp/screens SYSROOT=testdata/macbookair5,2   # and the real mode's, from a fixture
```

A dev `VERSION` (with "dev" in it) builds with `-tags omaboot_dev`: the console's dev-only commands
are in files with that build tag. A release `VERSION` leaves the tag off, so its binary doesn't
contain them; `make build TAGS=` leaves them out of a dev build too.

`VERSION`, `COMMIT` and `OMARCHY` (the Omarchy release reported when the image doesn't say) go
in through `-ldflags` (see the Makefile); the version shows at the bottom right of every screen,
e.g. "OmaBoot? Live 0.2.0 dev 4e1c9a2", and in the report's `source.version`.

The scrub check (`tools/scrub-check.sh`, the last step of `make check`) fails on private-network
addresses other than the usual examples, home-directory paths, private keys and report keys, and
on the maintainers' own host names, read from a patterns file outside the repo
(`$OMABOOT_SCRUB_PATTERNS`; skipped with a notice when there is none).

Unit tests cover only the brittle core: identification on the fixtures, each check's verdict
rules and the quick pass on the three Macs, building a report that passes `report.Check`, the
safety guard and the disk classification, the pacing, the glyph check, the static address
check, one report per run, the console's parsing and completion, and the console guard (a run
with a result changed in the console never reaches DoesItOmarchy.com).

## Layout

| Path | |
|---|---|
| `cmd/boot-live` | The binary: flags, the safety guard, console setup and restore, the Bubble Tea program, `--auto`. |
| `internal/app` | The real run: setup, the backend behind the screens, `--auto`. |
| `internal/fsread` | Finds a file in a disk's top folder by reading its partition table and file system (FAT, exFAT, HFS+) raw, never mounting (tested on real images). |
| `internal/safety` | The live-system guard, internal disks read-only, the thermal watchdog (tested). |
| `internal/identify` | Hardware probe and matching on the catalog snapshot (tested). |
| `internal/checks` | The quick pass, one check per criterion, the report's items (tested). |
| `internal/bootwatch` | The boot.watch test's client: echo, verified stream and sink, sign-in page detection (tested). |
| `internal/network` | Links, routes, Wi-Fi through iwd over D-Bus. |
| `internal/send` | The report, target.conf, submit, save to external storage (tested). |
| `internal/sysroot` | Reads system files under a root, so fixtures stand in for a Mac. |
| `helpers/egl-probe` | The GPU helper (C). |
| `testdata/` | Fixture trees of the three test Macs and a QEMU guest, and their generator. |
| `internal/console` | Linux VT detection, palette, font choice from EDID, `setfont`, restore. |
| `internal/grid` | The character grid screens draw on; palette; rows to escape codes. |
| `internal/ui` | The frame (header, title, step track, hint bar, version), the six S1 steps (plus the static IP form and the read-only report), the full log, the quit question, the screensaver, the console's box and keys; a backend drives them. |
| `internal/command` | The console's command registry: one file per command, registered from `init`; parsing, completion, history, the run's marks (tested). Dev-only commands carry the `omaboot_dev` build tag. |
| `internal/boot` | The Boot layer: Boot's sprite, expressions and idle life, his typed lines, the band effects. |
| `internal/pace` | Plays results back at a readable pace (tested). |
| `internal/i18n` | Every user-facing string goes through a gettext catalog. |
| `internal/glyphs` | Reads PSF fonts; its test checks every printable character against omaboot-v (tested). |
| `internal/demo` | The scripted MacBookAir5,2 run and its backend. |
| `assets/` | The catalog snapshot and Mac names built into the binary (`make snapshot`), and the fonts. |
| `locales/` | `boot-live.pot` and the catalogs (`<lang>/LC_MESSAGES/boot-live.po`); `make po` regenerates them from the source. English is the source language. |
| `assets/fonts/` | omaboot-v, our build of Terminus (see `NOTICE`). |
| `tools/` | `i18n-extract` (catalogs), `screens` (screen dumps), `testcd` (the OMABOOT_TEST disc's files) and `testfile` (the test file). |

## Translation

Every string goes through `i18n.T` (or `i18n.N` for plurals, `i18n.M` to mark text kept in
data). The msgid is the English text. `make po` writes `locales/boot-live.pot`; a translation is a
new `locales/<lang>/LC_MESSAGES/boot-live.po` made from it (Poedit, Weblate, `msginit`). Format
arguments can be reordered with `%[2]d`. Translations are checked against the font like
everything else, so a language needs its letters in omaboot-v.

## How it ships

The OmaBoot? images build this repo at a pinned commit into one static binary, install it with
the fonts in `/usr/share/omaboot/`, compile `helpers/egl-probe/egl-probe.c` against the image's
Mesa into `/usr/lib/omaboot/egl-probe`, write `target.conf`, and start it on tty1 (OmaBoot?
Live), or run it from a signed `BOOTLIVE` drive before the installer (OmaBoot? Omarchy). That
integration lives in the image fork, not here.

## Licence

Code: MIT ([LICENSE](LICENSE)). Fonts: omaboot-v is modified Terminus Font, SIL Open Font
License 1.1 ([assets/fonts/OFL.txt](assets/fonts/OFL.txt), [NOTICE](assets/fonts/NOTICE)).
