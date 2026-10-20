# Working on boot-live

Notes for anyone changing boot-live, people and coding agents alike: how the code is laid out, how
a run works inside, the contract with the image that carries it, and the project's conventions.
The [README](README.md) is the public overview.

## Conventions

- **Words.** The site is always written "DoesItOmarchy.com" (shown addresses too). The tagline is
  "a comprehensive Intel Mac test suite for Omarchy", word for word. Macs are named
  "Name [Identifier]", e.g. MacBook Air (13-inch, Mid 2012) [MacBookAir5,2]. On screen, saving
  says "external storage", the drive OmaBoot? runs from is "the OmaBoot? drive", and "stick" is
  left for USB devices in general.
- **Every user-facing string** goes through `i18n.T` (`i18n.N` for plurals, `i18n.M` for text
  kept in data); the msgid is the English text. `make po` regenerates the catalogs; `make check`
  fails when they're out of date.
- **Commits**: author Carl Hoyer <carl@hoyer.ca>; a `Co-Authored-By:` line for an agent is
  welcome, links to private agent sessions are not (this repo is public).
- **Unit tests only for the brittle core**: identification on the fixtures, each check's verdict
  rules and the quick pass on the three Macs, building a report that passes `report.Check`, the
  safety guard and disk classification, the pacing, the glyph check, the static address check,
  one report per run, the console's parsing and completion, and the console guard (a run with a
  result changed in the console never reaches DoesItOmarchy.com), and the keyboard layout macOS
  left in NVRAM. Everything else is covered end
  to end (the image's QEMU smoke tests and runs on real Macs).
- **Screens for review** are rendered with `make screens` at 160×45, 16 px, and shown 1:1.
- **No private names.** `make check` ends with the scrub check (`tools/scrub-check.sh`): it fails
  on private-network addresses other than the usual examples, home-directory paths, private keys
  and report keys, and on the maintainers' own host names, read from a patterns file outside the
  repo (`$OMABOOT_SCRUB_PATTERNS`; skipped with a notice when there's none).
- **Before a release**, refresh the built-in catalog from production: `make snapshot`.

## Build and check

```sh
make build    # static binary (CGO_ENABLED=0) with version and commit: dist/boot-live
make helper   # the GPU helper against this machine's Mesa: dist/egl-probe (needs Mesa's headers)
make snapshot # refresh assets/snapshot.json and macs.json (SNAPSHOT_FROM=, default https://doesitomarchy.com)
make check    # go vet, tests (with and without the dev tag), catalogs up to date, gofmt, scrub check
make screens OUT=/tmp/screens                                # every demo screen as .txt, .ans and .png
make screens OUT=/tmp/screens SYSROOT=testdata/macbookair5,2 # and the real mode's, from a fixture
```

`VERSION`, `COMMIT` and `OMARCHY` (the Omarchy release reported when the image doesn't say) go in
through `-ldflags` (see the Makefile); the version shows at the bottom right of every screen and in
the report's `source.version`. A `VERSION` with "dev" in it builds with `-tags omaboot_dev`: the
console's dev-only commands are in files with that build tag, so a release binary doesn't contain
them (`make build TAGS=` leaves them out of a dev build too).

## Layout

| Path | |
|---|---|
| `cmd/boot-live` | The binary: flags, the safety guard, console setup and restore, the Bubble Tea program, `--auto`. |
| `internal/app` | The real run: setup, the backend behind the screens, `--auto`, bug reports. |
| `internal/safety` | The live-system guard, internal disks read-only, the thermal watchdog (tested). |
| `internal/identify` | Hardware probe and matching on the catalog snapshot (tested). |
| `internal/checks` | The quick pass, one check per criterion, the report's items (tested). |
| `internal/handson` | The hands-on tests. |
| `internal/bootwatch` | The boot.watch client: echo, verified stream and sink, sign-in page detection (tested). |
| `internal/network` | Links, routes, Wi-Fi through iwd over D-Bus. |
| `internal/send` | The report, `target.conf`, submit, save to external storage, bug reports (tested). |
| `internal/plan` | Run plans (`omaboot-plan.yaml` on the stick). |
| `internal/fsread` | Finds a file in a disk's top folder by reading its partition table and file system (FAT, exFAT, HFS+) raw, never mounting (tested on real images). |
| `internal/sysroot` | Reads system files under a root, so fixtures stand in for a Mac. |
| `internal/console` | Linux VT detection, palette, font choice from EDID, `setfont`, restore. |
| `internal/keyboard` | The keyboard layouts Hello offers (Omarchy installer's names and console keymaps) and the default from macOS's `prev-lang:kbd` in NVRAM (tested). |
| `internal/grid` | The character grid screens draw on; palette; rows to escape codes. |
| `internal/ui` | The frame, the steps, the full log, the quit question, the screensaver, the console's box and keys; a backend drives them. |
| `internal/command` | The console's commands: one file per command, registered from `init`; parsing, completion, history, the run's marks (tested). Dev-only commands carry the `omaboot_dev` build tag. |
| `internal/boot` | Boot: his sprite, expressions and idle life, his typed lines, the band effects. |
| `internal/sounds` | Boot's sounds: short, quiet 8-bit blips and chimes. |
| `internal/icons` | The symbols omaboot-v carries beyond text (ports drawn two cells wide). |
| `internal/buildinfo` | The version, set at build time. |
| `internal/pace` | Plays results back at a readable pace (tested). |
| `internal/i18n` | The gettext catalogs. |
| `internal/glyphs` | Reads PSF fonts; its test checks every printable character against omaboot-v (tested). |
| `internal/demo` | The scripted MacBookAir5,2 run and its backend. |
| `helpers/egl-probe` | The GPU helper (C). |
| `assets/` | The catalog snapshot and Mac names built into the binary (`make snapshot`), and the fonts (omaboot-v, our build of Terminus; see `NOTICE`). |
| `locales/` | `boot-live.pot` and the catalogs (`<lang>/LC_MESSAGES/boot-live.po`). English is the source language. |
| `testdata/` | Fixture trees of the three test Macs and a QEMU guest, and their generator. |
| `tools/` | `i18n-extract` (catalogs), `screens` (screen dumps), `testcd` and `testfile` (the test media), `scrub-check.sh`. |

## A run, inside

boot-live starts only on the OmaBoot? live system: an archiso boot (`/run/archiso`, or
`archisobasedir=` on the kernel command line) that carries `/usr/share/omaboot/`. Anywhere else it
refuses (exit 3) before touching anything, because the first thing it does is make the internal
disks read-only.

1. **Safety** (`internal/safety`). Every internal disk, its partitions and any device-mapper/md
   device on them is set read-only (`BLKROSET`, what `blockdev --setro` does) and read back from
   sysfs; if any stays writable, or is mounted read-write, the run stops on Hello. "Internal" is
   decided by bus, never by the removable flag: USB and SD stay writable; SATA, NVMe and anything
   unrecognised (Thunderbolt, FireWire) become read-only. A thermal watchdog reads the CPU and GPU
   sensors every second and stops any load 5 °C below a sensor's `temp*_crit`.
2. **Which Mac** (`internal/identify`). DMI (`product_name`, `board_name` = Apple's board ID,
   chassis), the CPU, memory, PCI functions with subsystem IDs and drivers, and USB devices (sticks
   left out) become a `match.Probe` for doesitomarchy's `pkg/match`, run on the built-in catalog
   snapshot. One exact match is shown; several give a pick list (plus "Not sure"); none gives the
   "not in the catalog yet" path, where the report can still be saved.
3. **Network** (`internal/network`). Ethernet and USB tethering come up by themselves
   (systemd-networkd); online means a default route and the target's name resolving. Otherwise a
   Wi-Fi list from iwd over D-Bus, a masked password handed to iwd through its agent, connect, wait
   for DHCP. A static address writes `/run/systemd/network/10-omaboot-static-<iface>.network` and
   reloads networkd, for this run only. Once online the catalog snapshot is refreshed from the
   target (`If-None-Match`), and boot.watch is asked for one echo over plain HTTP: a sign-in page
   answering for it is reported. The echo never decides whether the Mac is online.
4. **Quick pass** (`internal/checks`): boot path, display, graphics (the GPU helper), GPU power-down
   (two GPUs), battery, the audio codec, Wi-Fi and Ethernet (seen only; their hands-on tests decide),
   thermal sensors under a CPU load, fans, keyboard backlight and light sensor. Each check's
   pass/partial/fail rule is written next to it against the criterion's wording. The network
   hands-on tests move their data through bootwatchd on a socket bound to the interface: 64 KB and
   256 KB down and 64 KB up, every byte checked against a stream both sides compute from a seed the
   Mac picks, then a 1 MB part cut at 2 s for the speed. Every other criterion that applies goes in
   the report as not_tested (`live-limit` where a live boot can't decide it, else
   `not-in-profile`). A fix counts only when it took effect (the radeon panel fix by its kernel log
   line; the applesmc fix when applesmc loaded from `updates/` and registered
   `smc::kbd_backlight`). Before the first graphics check a "Dark screen ahead?" notice counts down.
5. **Report** (`internal/send`). One report per run, sent or saved only on Finish: a schema v1
   `report.File` (`context: live`, identifier, configuration, hardware probe, items, extras, fixes,
   Omarchy version and image, kernel, `source: boot-live`, the name to credit and the consent
   notice shown), checked with `report.Check` first. Submit POSTs it to `<target>/api/v1/reports`
   with `Authorization: Bearer <key>`; once a code came back a second send is refused. Saving
   writes `results/<identifier>-<code or time>.json` on the drive labelled `BOOTLIVE`, else on the
   OmaBoot? drive's own `OMABOOT` reports partition (mounted if needed, never formatted).

The GPU helper (`helpers/egl-probe/`, C) opens GBM on each render node, makes an OpenGL ES context
with no window system, renders one frame offscreen, reads a pixel back and times a 1080p
compositor-like load, printing `key=value` lines. Without it the GPU criteria are not_tested.

**Test media.** Any disc, stick, card or drive does for the storage tests. Two files at
`get.omaboot.live/test/` (signed `SHA256SUMS`) also prove the bytes came back right:
`omaboot-test.iso`, the OMABOOT_TEST disc for the optical drive test (`go run ./tools/testcd DIR`,
then `xorriso -as mkisofs -V OMABOOT_TEST -iso-level 3`), and `OMABOOT-TEST.BIN`, the test file for
the USB, SD card and FireWire tests (`go run ./tools/testfile DIR`). The tests find the file
without mounting anything (`internal/fsread`) and check every byte.

## The contract with the image

The image (built by omaboot/omaboot-iso) installs the binary and the fonts under
`/usr/share/omaboot/`, compiles the GPU helper into `/usr/lib/omaboot/egl-probe`, and starts
boot-live on tty1. It writes:

- `/usr/share/omaboot/target.conf`: `url=` (the DoesItOmarchy site), `key=` (the boot-live source
  key for it), `watch=` (the bootwatchd the network tests use; default `https://boot.watch`),
  `bugs=` and `bugkey=` (where bug reports go); `#` starts a comment. Without a key, Submit is
  greyed out with a note and saving still works. `--target`, `--key` and `--watch` override them.
- `/usr/share/omaboot/omarchy-version` (e.g. `4.0.6`) and `/usr/share/omaboot/image`. Without
  them the report takes the Omarchy version from `/etc/os-release` or the package database (else
  the built-in `OMARCHY`), and the image name from `/usr/share/omaboot/build`.

**Exit status:** 0 done (or quit), 1 failed, 2 bad flags, 3 refused (not the live system, or the
disks couldn't be made read-only), 10 the person chose "Install Omarchy now" or "Go to the
installer" (OmaBoot? Omarchy: the image's script starts the installer). Shutdown and Restart run
`systemctl poweroff` / `reboot` once the console is restored.

**`--auto`**, for the image's QEMU smoke test: no input; identify (the exact match, else the
best), use whatever network comes up within 20 s, run the quick pass, then submit if there's a
target, a key and a network (and save a copy), else save. Progress lines (`boot-live: …`) go to
stderr and the serial port; the run ends with exactly one line on stdout and on `/dev/ttyS0`:

```
OMABOOT-AUTO: submitted <code>
OMABOOT-AUTO: saved results/<identifier>-<time>.json
OMABOOT-AUTO: failed <reason>
```

## Developing without a Mac

`--sysroot DIR` reads every `/sys`, `/proc`, `/dev` and `/etc` file under DIR instead of the
machine's, and writes nothing: the disk step is a dry run, Wi-Fi is simulated from the tree's
`iwd-networks.txt`, a static address is only pretended, the GPU helper's output comes from its
`egl-probe.txt`, saving needs `--stick DIR`, and Shutdown and Restart only say so. The trees in
`testdata/` are generated by `testdata/gen-fixtures.py` from real probe data of a MacBookAir5,2
(applesmc fix), an iMac10,1 (radeon fix), an iMac18,3, and a QEMU guest that isn't a Mac.

```sh
./dist/boot-live --sysroot testdata/macbookair5,2 --term --stick /tmp/stick
./dist/boot-live --sysroot testdata/imac10,1 --auto --stick /tmp/stick --target http://localhost:8080 --key doi_…
```

`--demo` drives the same steps with a scripted backend on a MacBookAir5,2 whose results are made
up. `--plain` turns Boot off; `--edition live|omarchy` picks the product name and endings (only
OmaBoot? Omarchy offers "Install Omarchy now"); `--term` is for a terminal emulator (no console
font or palette setup). `boot-live --help` lists every flag.

## On the Linux console

On a virtual console (`TERM=linux`) boot-live sets its 16-colour palette (`GIO_CMAP`/`PIO_CMAP`,
`\e]P` as a fallback) and restores the old one on exit; picks omaboot-v at 16, 24 or 32 px from
the screen's physical size (the panel's EDID size and the framebuffer resolution), loads it with
`setfont` and restores the original font on exit; loads the keyboard layout (`loadkeys`: macOS's
last one from NVRAM, else US; Tab on Hello picks another) with the lines that give Ctrl+↑/↓ their
own sequences over it, and puts the keymap it started with back on exit; hides the cursor and redraws only the lines that
changed, with animations kept inside Boot's area at 15 fps. Fonts are looked up in
`/usr/share/omaboot/fonts/` (the images), then `assets/fonts/` (this repo).

The console (**`**, or Ctrl+` from a text field) has its commands documented on
[omaboot.live](https://omaboot.live/docs/console/). Every command and its output go to the full
log; the report lists the commands that redo or change a result. On a screen waiting for input,
Boot dozes after 5 minutes and the screen blanks after 15; the key that wakes it does nothing
else. Never during the quick pass.

## Translation

A translation is a new `locales/<lang>/LC_MESSAGES/boot-live.po` made from `locales/boot-live.pot`
(Poedit, Weblate, `msginit`). Format arguments can be reordered with `%[2]d`. Translations are
checked against the font like everything else, so a language needs its letters in omaboot-v.
