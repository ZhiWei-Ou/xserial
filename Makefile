APP_NAME := xserial
APP_DIR := ./cmd/xserial
EXE_PATH := ./bin
RELEASE_DIR := ./dist
RELEASE_STAGE := $(RELEASE_DIR)/.stage
VERSION ?= $(shell git describe --tags --always --dirty)
VERSION_PKG := github.com/ZhiWei-Ou/xserial/internal/cmd
RELEASE_LDFLAGS := -s -w -X $(VERSION_PKG).buildVersion=$(VERSION)

TARGET_OS := darwin linux windows
TARGET_ARCH := amd64 arm64

.PHONY: dir
dir:
	@mkdir -p $(EXE_PATH)

.PHONY: test
test:
	go test ./...

.PHONY: build
build: dir
	@ext=""; \
	if [ "$$(go env GOOS)" = "windows" ]; then ext=".exe"; fi; \
	go build -o $(EXE_PATH)/$(APP_NAME)$$ext $(APP_DIR)

.PHONY: clean
clean:
	@rm -rf $(EXE_PATH)

.PHONY: release
release:
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_STAGE)
	@cp LICENSE $(RELEASE_STAGE)/LICENSE
	@set -eu; \
	for os in $(TARGET_OS); do \
		for arch in $(TARGET_ARCH); do \
			name="$(APP_NAME)_$(VERSION)_$${os}_$${arch}"; \
			binary="$(APP_NAME)"; \
			if [ "$${os}" = "windows" ]; then binary="$(APP_NAME).exe"; fi; \
			CGO_ENABLED=0 GOOS="$${os}" GOARCH="$${arch}" go build -trimpath -ldflags "$(RELEASE_LDFLAGS)" -o "$(RELEASE_STAGE)/$${binary}" $(APP_DIR); \
			if [ "$${os}" = "windows" ]; then \
			(cd $(RELEASE_STAGE) && zip -q "../$${name}.zip" "$${binary}" LICENSE); \
			else \
			tar -C $(RELEASE_STAGE) -czf "$(RELEASE_DIR)/$${name}.tar.gz" "$${binary}" LICENSE; \
			fi; \
			rm -f "$(RELEASE_STAGE)/$${binary}"; \
		done; \
	done
	@cd $(RELEASE_DIR) && if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum $(APP_NAME)_* > checksums.txt; \
	else \
		shasum -a 256 $(APP_NAME)_* > checksums.txt; \
	fi
	@rm -rf $(RELEASE_STAGE)

.PHONY: darwin-amd64
darwin-amd64: dir
	GOOS=darwin GOARCH=amd64 go build -o $(EXE_PATH)/$(APP_NAME)-darwin-amd64 $(APP_DIR)

.PHONY: darwin-arm64
darwin-arm64: dir
	GOOS=darwin GOARCH=arm64 go build -o $(EXE_PATH)/$(APP_NAME)-darwin-arm64 $(APP_DIR)

.PHONY: linux-amd64
linux-amd64: dir
	GOOS=linux GOARCH=amd64 go build -o $(EXE_PATH)/$(APP_NAME)-linux-amd64 $(APP_DIR)

.PHONY: linux-arm64
linux-arm64: dir
	GOOS=linux GOARCH=arm64 go build -o $(EXE_PATH)/$(APP_NAME)-linux-arm64 $(APP_DIR)

.PHONY: windows-amd64
windows-amd64: dir
	GOOS=windows GOARCH=amd64 go build -o $(EXE_PATH)/$(APP_NAME)-windows-amd64.exe $(APP_DIR)

.PHONY: windows-arm64
windows-arm64: dir
	GOOS=windows GOARCH=arm64 go build -o $(EXE_PATH)/$(APP_NAME)-windows-arm64.exe $(APP_DIR)

.PHONY: all
all: darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64
