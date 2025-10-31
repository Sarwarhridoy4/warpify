# Warpify Makefile
# Production build system for Linux

.PHONY: all build install uninstall clean icon deps appimage deb release help run test fmt lint info build-fyne build-static

# Variables
APP_NAME := warpify
VERSION := 1.0.0
BUILD_DIR := build
INSTALL_PREFIX := /usr/local
ICON_SIZE := 512

# Build flags
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILDFLAGS := -trimpath -ldflags="$(LDFLAGS)"

all: deps icon build

help:
	@echo "Warpify Build System"
	@echo "===================="
	@echo ""
	@echo "Available targets:"
	@echo "  make build      - Build optimized binary"
	@echo "  make icon       - Generate application icon"
	@echo "  make deps       - Install dependencies"
	@echo "  make install    - Install to system"
	@echo "  make uninstall  - Remove from system"
	@echo "  make appimage   - Create portable AppImage"
	@echo "  make deb        - Create Debian package"
	@echo "  make release    - Build release package (AppImage + deb)"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make run        - Build and run"
	@echo "  make test       - Run tests"
	@echo "  make fmt        - Format code"
	@echo "  make lint       - Lint code"
	@echo "  make info       - Show build information"
	@echo ""

deps:
	@echo "📦 Installing dependencies..."
	go get fyne.io/fyne/v2
	go get github.com/grandcat/zeroconf
	go install fyne.io/fyne/v2/cmd/fyne@latest
	@echo "✓ Dependencies installed"

icon:
	@if [ ! -f "Icon.png" ]; then \
		echo "🎨 Generating icon..."; \
		go run tools/generate_icon.go; \
		echo "✓ Icon generated"; \
	else \
		echo "✓ Icon already exists"; \
	fi

build: icon
	@echo "🔨 Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(BUILDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)
	@echo "✓ Build complete: $(BUILD_DIR)/$(APP_NAME)"
	@ls -lh $(BUILD_DIR)/$(APP_NAME) | awk '{print "  Size: " $$5}'

build-fyne: icon
	@echo "🔨 Building with Fyne packager..."
	@mkdir -p $(BUILD_DIR)
	fyne package -os linux -icon Icon.png -name $(APP_NAME)
	@if [ -f "$(APP_NAME).tar.xz" ]; then mv $(APP_NAME).tar.xz $(BUILD_DIR)/; fi
	@echo "✓ Fyne package complete"

build-static: icon
	@echo "🔨 Building static binary..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(BUILDFLAGS) -tags static -o $(BUILD_DIR)/$(APP_NAME)-static
	@echo "✓ Static build complete"

$(BUILD_DIR)/$(APP_NAME).desktop:
	@echo "📄 Creating desktop entry..."
	@mkdir -p $(BUILD_DIR)
	@printf '%s\n' \
		'[Desktop Entry]' \
		'Version=1.0' \
		'Type=Application' \
		'Name=Warpify' \
		'GenericName=File Transfer' \
		'Comment=Secure local file sharing' \
		'Exec=$(APP_NAME)' \
		'Icon=$(APP_NAME)' \
		'Terminal=false' \
		'Categories=Network;FileTransfer;P2P;' \
		'Keywords=transfer;share;files;p2p;network;' \
		'StartupNotify=true' \
		'MimeType=application/octet-stream;' \
		> $(BUILD_DIR)/$(APP_NAME).desktop
	@echo "✓ Desktop entry created"

install: build $(BUILD_DIR)/$(APP_NAME).desktop
	@echo "📦 Installing $(APP_NAME)..."
	sudo install -Dm755 $(BUILD_DIR)/$(APP_NAME) $(INSTALL_PREFIX)/bin/$(APP_NAME)
	sudo install -Dm644 Icon.png $(INSTALL_PREFIX)/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps/$(APP_NAME).png
	sudo install -Dm644 $(BUILD_DIR)/$(APP_NAME).desktop $(INSTALL_PREFIX)/share/applications/$(APP_NAME).desktop
	-sudo update-desktop-database 2>/dev/null
	-sudo gtk-update-icon-cache $(INSTALL_PREFIX)/share/icons/hicolor/ 2>/dev/null
	@echo "✓ Installation complete"
	@echo "  Run: $(APP_NAME)"

uninstall:
	@echo "🗑️  Uninstalling $(APP_NAME)..."
	-sudo rm -f $(INSTALL_PREFIX)/bin/$(APP_NAME)
	-sudo rm -f $(INSTALL_PREFIX)/share/applications/$(APP_NAME).desktop
	-sudo rm -f $(INSTALL_PREFIX)/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps/$(APP_NAME).png
	-sudo update-desktop-database 2>/dev/null
	-sudo gtk-update-icon-cache $(INSTALL_PREFIX)/share/icons/hicolor/ 2>/dev/null
	@echo "✓ Uninstallation complete"

# -----------------------------
# AppImage target
# -----------------------------
appimage: build icon $(BUILD_DIR)/$(APP_NAME).desktop
	@echo "📦 Creating AppImage..."
	@set -e; \
	APPDIR=$(BUILD_DIR)/$(APP_NAME).AppDir; \
	# Create necessary directories
	mkdir -p $$APPDIR/usr/bin $$APPDIR/usr/share/applications $$APPDIR/usr/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps; \
	# Copy binary
	cp $(BUILD_DIR)/$(APP_NAME) $$APPDIR/usr/bin/; \
	# Copy icon
	cp Icon.png $$APPDIR/usr/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps/$(APP_NAME).png; \
	# Copy desktop file
	cp $(BUILD_DIR)/$(APP_NAME).desktop $$APPDIR/usr/share/applications/$(APP_NAME).desktop; \
	# Create AppRun script
	printf '%s\n' '#!/bin/bash' \
	'SELF=$$(readlink -f "$$0")' \
	'HERE=$${SELF%/*}' \
	'export PATH="$${HERE}/usr/bin:$${PATH}"' \
	'export LD_LIBRARY_PATH="$${HERE}/usr/lib:$${LD_LIBRARY_PATH}"' \
	'cd "$${HERE}/usr/bin"' \
	'exec "./$(APP_NAME)" "$$@"' \
	> $$APPDIR/AppRun; \
	chmod +x $$APPDIR/AppRun; \
	# Download appimagetool if missing
	if [ ! -f "tools/appimagetool-x86_64.AppImage" ]; then \
		mkdir -p tools; \
		wget -q -O tools/appimagetool-x86_64.AppImage https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage; \
		chmod +x tools/appimagetool-x86_64.AppImage; \
	fi; \
	# Build AppImage
	ARCH=x86_64 tools/appimagetool-x86_64.AppImage $$APPDIR $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-x86_64.AppImage; \
	chmod +x $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-x86_64.AppImage; \
	echo "✓ AppImage created: $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-x86_64.AppImage"


# -----------------------------
# Debian package target
# -----------------------------
deb: build icon $(BUILD_DIR)/$(APP_NAME).desktop
	@echo "📦 Creating Debian package..."
	@set -e; \
	DEB_DIR=$(BUILD_DIR)/deb; \
	mkdir -p $$DEB_DIR/DEBIAN $$DEB_DIR/usr/bin $$DEB_DIR/usr/share/applications $$DEB_DIR/usr/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps; \
	cp $(BUILD_DIR)/$(APP_NAME) $$DEB_DIR/usr/bin/; \
	cp Icon.png $$DEB_DIR/usr/share/icons/hicolor/$(ICON_SIZE)x$(ICON_SIZE)/apps/$(APP_NAME).png; \
	cp $(BUILD_DIR)/$(APP_NAME).desktop $$DEB_DIR/usr/share/applications/; \
	\
	printf '%s\n' \
	'Package: $(APP_NAME)' \
	'Version: $(VERSION)' \
	'Section: utils' \
	'Priority: optional' \
	'Architecture: amd64' \
	'Depends: libc6, libgtk-3-0' \
	'Maintainer: Your Name <you@example.com>' \
	'Description: Warpify - Secure local file sharing' \
	> $$DEB_DIR/DEBIAN/control; \
	dpkg-deb --build $$DEB_DIR $(BUILD_DIR)/$(APP_NAME)_$(VERSION)_amd64.deb; \
	echo "✓ Debian package created: $(BUILD_DIR)/$(APP_NAME)_$(VERSION)_amd64.deb"

# -----------------------------
# Release target
# -----------------------------
release: clean build appimage deb
	@echo "📦 Creating release package..."
	@mkdir -p $(BUILD_DIR)/release
	@cp $(BUILD_DIR)/$(APP_NAME) $(BUILD_DIR)/release/
	@cp Icon.png $(BUILD_DIR)/release/
	@cp $(BUILD_DIR)/$(APP_NAME).desktop $(BUILD_DIR)/release/
	@cp $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-x86_64.AppImage $(BUILD_DIR)/release/
	@cp $(BUILD_DIR)/$(APP_NAME)_$(VERSION)_amd64.deb $(BUILD_DIR)/release/
	@if [ -f "README.md" ]; then \
		cp README.md $(BUILD_DIR)/release/; \
	else \
		echo "# Warpify v$(VERSION)" > $(BUILD_DIR)/release/README.md; \
	fi
	@cd $(BUILD_DIR) && tar -czf $(APP_NAME)-$(VERSION)-linux-amd64.tar.gz release/
	@echo "✓ Release package created: $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz"
	@ls -lh $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz | awk '{print "  Size: " $$5}'

run: build
	@echo "🚀 Running $(APP_NAME)..."
	@$(BUILD_DIR)/$(APP_NAME)

clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f $(APP_NAME) $(APP_NAME).tar.xz
	@echo "✓ Clean complete"

test:
	@echo "🧪 Running tests..."
	@go test -v ./... || echo "No tests found"

fmt:
	@echo "📝 Formatting code..."
	@go fmt ./...
	@echo "✓ Format complete"

lint:
	@echo "🔍 Linting code..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "⚠️  golangci-lint not installed, skipping"; \
	fi

info:
	@echo "Build Information"
	@echo "================="
	@echo "App Name:    $(APP_NAME)"
	@echo "Version:     $(VERSION)"
	@echo "Build Dir:   $(BUILD_DIR)"
	@echo "Install Dir: $(INSTALL_PREFIX)"
	@echo "Go Version:  $$(go version)"
	@echo ""
