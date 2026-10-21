# boot-live: OmaBoot? Live's tester.

VERSION ?= 1.0.1
COMMIT  ?= $(shell git rev-parse --short=7 HEAD 2>/dev/null)
OMARCHY ?= 4.0.6
PKG     := github.com/omaboot/boot-live/internal/buildinfo
LDFLAGS := -s -w -X '$(PKG).Version=$(VERSION)' -X '$(PKG).Commit=$(COMMIT)' -X '$(PKG).Omarchy=$(OMARCHY)'
BIN     := dist/boot-live
# Dev builds carry the console's dev-only commands (force, goto, font, fps, target),
# which live in files built with the omaboot_dev tag. A dev VERSION turns the tag on; a release
# VERSION (no "dev") leaves it off, so its binary doesn't contain them. TAGS= overrides either way.
TAGS    ?= $(if $(findstring dev,$(VERSION)),omaboot_dev)

# Where `make snapshot` fetches the catalog from (a development build passes its staging site).
SNAPSHOT_FROM ?= https://doesitomarchy.com

.PHONY: build helper snapshot test check po demo screens clean

# One static binary: the images have no Go and no libc to match.
build:
	CGO_ENABLED=0 go build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/boot-live

# The GPU check's helper, built against this machine's Mesa: dist/egl-probe. The image build
# compiles helpers/egl-probe/egl-probe.c itself, against the image's Mesa, into
# /usr/lib/omaboot/egl-probe.
helper:
	mkdir -p dist
	cc -O2 -Wall -o dist/egl-probe helpers/egl-probe/egl-probe.c -lEGL -lGLESv2 -lgbm

# The catalog snapshot and the Macs' names built into the binary (assets/), for identifying a Mac
# with no network: make snapshot [SNAPSHOT_FROM=http://localhost:8080]
snapshot:
	curl -fsS $(SNAPSHOT_FROM)/api/v1/snapshot -o assets/snapshot.json.part
	curl -fsS $(SNAPSHOT_FROM)/api/v1/macs -o assets/macs.json.part
	mv assets/snapshot.json.part assets/snapshot.json
	mv assets/macs.json.part assets/macs.json

test:
	go test ./...

# What to run before handing a change over: vet, tests (pacing, glyph check, identification,
# checks, report, safety guard, console), catalogs up to date; vet and the console's tests again
# with the dev-only commands built in; gofmt; the scrub check (no private addresses or host names).
check:
	go vet ./...
	go vet -tags omaboot_dev ./...
	go test ./...
	go test -tags omaboot_dev ./internal/command/ ./internal/ui/
	go run ./tools/i18n-extract -check
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }
	@sh tools/scrub-check.sh

# Regenerate the message catalogs from the source.
po:
	go run ./tools/i18n-extract

# Try the screens in a terminal emulator (needs at least 120×40; 160×50 matches a MacBook).
# ` opens the console.
demo: build
	./$(BIN) --demo --term

# Draw the demo's screens to text, ANSI and PNG files: make screens OUT=/tmp/screens; with
# SYSROOT=testdata/macbookair5,2 also the real mode's screens from a fixture run.
OUT ?= screens
screens:
	go run -tags "$(TAGS)" ./tools/screens -out $(OUT) $(if $(SYSROOT),-sysroot $(SYSROOT))

clean:
	rm -rf dist screens
