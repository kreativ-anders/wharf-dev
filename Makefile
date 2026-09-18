# Wharf — build and test.
#
# The daemon is a single cross-compiled binary with no runtime dependencies,
# so `make cross` building for all three operating systems is the check that
# the "one codebase, minimal platform branching" claim still holds.

GO      ?= go
# INFO: gui/pubspec.yaml is the one version source; see dev/releasing.md.
VERSION ?= $(shell ./tool/version.sh describe 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BUILD   := build
# INFO: The app looks for wharfd.exe on Windows (gui/lib/daemon_launcher.dart).
EXE     := $(if $(filter Windows_NT,$(OS)),.exe,)
# INFO: DARWIN_UNIVERSAL=1 builds wharfd for Apple Silicon and Intel in one binary:
# a release Wharf.app is universal, so the daemon it embeds must be too.
UNIVERSAL := $(and $(filter 1,$(DARWIN_UNIVERSAL)),$(filter Darwin,$(shell uname)))

.PHONY: all build test spec race vet fmt cross clean run check gui gui-test gui-e2e gui-analyze \
	version bump release dmg

all: fmt vet test build

# INFO: Everything that can run unattended: both halves, and the guard that keeps
# the specs and the code honest.
check: vet test gui-analyze gui-test build gui-e2e

build:
	@mkdir -p $(BUILD)
	@# WARNING: go build will not overwrite a universal binary it did not write itself.
	@rm -f $(BUILD)/wharfd$(EXE)
ifneq ($(UNIVERSAL),)
	cd daemon && GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfd-arm64 ./cmd/wharfd
	cd daemon && GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfd-amd64 ./cmd/wharfd
	lipo -create -output $(BUILD)/wharfd $(BUILD)/wharfd-arm64 $(BUILD)/wharfd-amd64
	rm -f $(BUILD)/wharfd-arm64 $(BUILD)/wharfd-amd64
else
	cd daemon && $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfd$(EXE)   ./cmd/wharfd
endif
	cd daemon && $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfctl$(EXE) ./cmd/wharfctl

test:
	cd daemon && $(GO) test ./...

# INFO: The spec-to-test coverage matrix. See CLAUDE.md §1.
spec:
	@cd daemon && $(GO) test ./internal/specsync/ -run TestSpecCoverage -v -count=1 | \
		sed -e 's/^ *specsync_test.go:[0-9]*://' -e '/^=== RUN/d' -e '/^ok  /d'

race:
	cd daemon && $(GO) test -race -count=1 ./...

vet:
	cd daemon && $(GO) vet ./...

fmt:
	cd daemon && gofmt -l -w .

# INFO: Every supported target must build from the same source, unmodified.
cross:
	@set -e; for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${t%%/*}; arch=$${t##*/}; \
		printf '%-16s' "$$t"; \
		(cd daemon && GOOS=$$os GOARCH=$$arch $(GO) build -o /dev/null ./...) && echo ok; \
	done

# INFO: Run the daemon against a throwaway root with elevation disabled, so no
# password prompt is raised.
run: build
	@mkdir -p $(BUILD)/dev/root
	./$(BUILD)/wharfd --root $(BUILD)/dev/root \
		--elevator direct --socket /tmp/wharf-dev.sock --log-level debug

# ---------------------------------------------------------------------- GUI

FLUTTER ?= flutter

gui-analyze:
	cd gui && $(FLUTTER) analyze

# INFO: Unit and widget tests only: hermetic, no daemon binary needed.
gui-test:
	cd gui && $(FLUTTER) test --exclude-tags e2e

# INFO: Drives the real daemon binary, so build it first.
gui-e2e: build
	cd gui && $(FLUTTER) test --tags e2e

# INFO: Launch the app against a throwaway root. The app starts its own daemon, as
# it does for a user; WHARFD_ARGS only disables elevation, so no password
# prompt is raised.
DEV_ROOT := $(PWD)/$(BUILD)/dev/root
FLUTTER_DEVICE ?= $(shell uname | tr 'A-Z' 'a-z' | sed 's/darwin/macos/')

gui: build
	@mkdir -p $(DEV_ROOT)/www
	cd gui && WHARF_ROOT=$(DEV_ROOT) \
		WHARFD_ARGS="--elevator direct" \
		$(FLUTTER) run -d $(FLUTTER_DEVICE)

# ------------------------------------------------------------------ release

# INFO: The version this checkout builds as.
version:
	@./tool/version.sh describe

# INFO: BUMP is patch, minor, major or an exact X.Y.Z. `bump` only edits
# gui/pubspec.yaml; `release` also commits and tags, and never pushes.
bump:
	@$(if $(BUMP),,$(error BUMP=patch|minor|major|X.Y.Z is required))./tool/version.sh bump $(BUMP)

release:
	@$(if $(BUMP),,$(error BUMP=patch|minor|major|X.Y.Z is required))tag=$$(./tool/version.sh release $(BUMP)) && \
		echo "Tagged $$tag. Publishing it starts the release workflow:" && \
		echo "  git push --atomic origin HEAD:main $$tag"

# INFO: The release build of Wharf.app in a DMG, through the same script the release
# workflow runs: signed and notarised when the credentials are there, unsigned
# otherwise (dev/releasing.md §5).
dmg:
	cd gui && $(FLUTTER) build macos --release
	./packaging/macos/build_dmg.sh

clean:
	rm -rf $(BUILD)
	cd gui && $(FLUTTER) clean > /dev/null 2>&1 || true
