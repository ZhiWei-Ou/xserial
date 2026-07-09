APP_NAME := xserial
APP_DIR := ./cmd/xserial
EXE_PATH := ./bin

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
