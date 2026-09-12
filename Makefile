# Wharf — build and test.
#
# The daemon is a single cross-compiled binary with no runtime dependencies,
# so `make cross` building for all three operating systems is the check that
# the "one codebase, minimal platform branching" claim still holds.

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BUILD   := build
# The app looks for wharfd.exe on Windows (gui/lib/daemon_launcher.dart).
EXE     := $(if $(filter Windows_NT,$(OS)),.exe,)

.PHONY: all build test spec race vet fmt cross clean run check gui gui-test gui-e2e gui-analyze

all: fmt vet test build

# Everything that can run unattended: both halves, and the guard that keeps
# the specs and the code honest.
check: vet test gui-analyze gui-test build gui-e2e

build:
	@mkdir -p $(BUILD)
	cd daemon && $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfd$(EXE)   ./cmd/wharfd
	cd daemon && $(GO) build -ldflags "$(LDFLAGS)" -o ../$(BUILD)/wharfctl$(EXE) ./cmd/wharfctl

test:
	cd daemon && $(GO) test ./...

# The spec-to-test coverage matrix. See CLAUDE.md §1.
spec:
	@cd daemon && $(GO) test ./internal/specsync/ -run TestSpecCoverage -v -count=1 | \
		sed -e 's/^ *specsync_test.go:[0-9]*://' -e '/^=== RUN/d' -e '/^ok  /d'

race:
	cd daemon && $(GO) test -race -count=1 ./...

vet:
	cd daemon && $(GO) vet ./...

fmt:
	cd daemon && gofmt -l -w .

# Every supported target must build from the same source, unmodified.
cross:
	@set -e; for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${t%%/*}; arch=$${t##*/}; \
		printf '%-16s' "$$t"; \
		(cd daemon && GOOS=$$os GOARCH=$$arch $(GO) build -o /dev/null ./...) && echo ok; \
	done

# Run the daemon against a throwaway root with elevation disabled, so no
# password prompt is raised and the real /etc/hosts is never touched.
run: build
	@mkdir -p $(BUILD)/dev/root
	@printf '127.0.0.1\tlocalhost\n' > $(BUILD)/dev/hosts
	./$(BUILD)/wharfd --root $(BUILD)/dev/root --hosts $(BUILD)/dev/hosts \
		--elevator direct --socket /tmp/wharf-dev.sock --log-level debug

# ---------------------------------------------------------------------- GUI

FLUTTER ?= flutter

gui-analyze:
	cd gui && $(FLUTTER) analyze

# Unit and widget tests only: hermetic, no daemon binary needed.
gui-test:
	cd gui && $(FLUTTER) test --exclude-tags e2e

# Drives the real daemon binary, so build it first.
gui-e2e: build
	cd gui && $(FLUTTER) test --tags e2e

# Launch the app against a throwaway root. The app starts its own daemon, as
# it does for a user; WHARFD_ARGS only points that daemon at a scratch hosts
# file with elevation disabled, so nothing real is touched.
DEV_ROOT := $(PWD)/$(BUILD)/dev/root
DEV_HOSTS := $(PWD)/$(BUILD)/dev/hosts
FLUTTER_DEVICE ?= $(shell uname | tr 'A-Z' 'a-z' | sed 's/darwin/macos/')

gui: build
	@mkdir -p $(DEV_ROOT)/www
	@printf '127.0.0.1\tlocalhost\n' > $(DEV_HOSTS)
	cd gui && WHARF_ROOT=$(DEV_ROOT) \
		WHARFD_ARGS="--hosts $(DEV_HOSTS) --elevator direct" \
		$(FLUTTER) run -d $(FLUTTER_DEVICE)

clean:
	rm -rf $(BUILD)
	cd gui && $(FLUTTER) clean > /dev/null 2>&1 || true
