# Build the Marionette desktop app.
#
#   make test      run the tests
#   make build     binary for this machine     -> dist/marionette
#   make app       macOS universal .app        -> dist/Marionette.app  (macOS only)
#   make release   every release archive       -> dist/*.zip, dist/*.tar.gz

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLIST_VERSION := $(patsubst v%,%,$(VERSION))
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG := ./cmd/marionette
APP := dist/Marionette.app

.PHONY: test build app release clean

test:
	go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" -o dist/marionette $(PKG)

app:
	rm -rf $(APP)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o dist/darwin-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o dist/darwin-amd64 $(PKG)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	lipo -create -output $(APP)/Contents/MacOS/marionette dist/darwin-arm64 dist/darwin-amd64
	rm dist/darwin-arm64 dist/darwin-amd64
	sed 's/__VERSION__/$(PLIST_VERSION)/g' packaging/macos/Info.plist > $(APP)/Contents/Info.plist
	cp packaging/macos/AppIcon.icns $(APP)/Contents/Resources/
	codesign --force --deep --sign - $(APP)

release: app
	cd dist && ditto -c -k --keepParent Marionette.app Marionette-macos.zip
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS) -H=windowsgui" -o dist/windows/marionette.exe $(PKG)
	cd dist/windows && zip -q ../Marionette-windows-amd64.zip marionette.exe
	for arch in amd64 arm64; do \
	  GOOS=linux GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/linux-$$arch/marionette $(PKG) && \
	  tar -czf dist/marionette-linux-$$arch.tar.gz -C dist/linux-$$arch marionette || exit 1; \
	done

clean:
	rm -rf dist
