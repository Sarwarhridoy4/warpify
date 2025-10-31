# 🚀 Warpify - Secure Local File Transfer

Fast, secure, and beautiful peer-to-peer file sharing for local networks.

![Version](https://img.shields.io/badge/version-1.0.0-blue)
![License](https://img.shields.io/badge/license-MIT-green)
![Platform](https://img.shields.io/badge/platform-Linux-orange)

## ✨ Features

- 🔒 **Secure**: SHA-256 checksum verification, input validation
- ⚡ **Fast**: 4MB buffer with real-time speed indicators
- 🎨 **Beautiful UI**: Modern, intuitive interface with progress tracking
- 🔍 **Auto-Discovery**: Finds devices automatically on your network
- 📦 **No Server**: Direct peer-to-peer transfers
- 🖥️ **Cross-Device**: Works across different Linux machines

## 📋 Prerequisites

- Go 1.19 or higher
- Linux (Ubuntu, Fedora, Arch, etc.)
- GTK3 development libraries

### Install Dependencies

**Ubuntu/Debian:**

```bash
sudo apt update
sudo apt install -y golang-go gcc libgl1-mesa-dev xorg-dev
```

**Fedora/RHEL:**

```bash
sudo dnf install -y golang gcc mesa-libGL-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
```

**Arch Linux:**

```bash
sudo pacman -S go gcc libgl libxcursor libxrandr libxinerama libxi
```

## 🔨 Building from Source

The Makefile handles builds, icons, AppImage creation, Debian packaging, and releases.

### Available Make Targets

```bash
make help
```

- `make build` – Build optimized binary with icon
- `make icon` – Generate application icon
- `make deps` – Install Go dependencies
- `make install` – Install system-wide
- `make uninstall` – Remove from system
- `make appimage` – Create portable AppImage
- `make deb` – Create Debian package (.deb)
- `make release` – Build release package (AppImage + .deb + tarball)
- `make clean` – Clean build artifacts
- `make run` – Build and run
- `make test` – Run Go tests
- `make fmt` – Format Go code
- `make lint` – Lint Go code
- `make info` – Show build information

### Quick Build

```bash
make build
```

### Run Application

```bash
make run
```

### Full Release (AppImage + Debian + Tarball)

```bash
make release
# Output in build/release/
```

## 📦 Installation

### Method 1: Using Make

```bash
sudo make install
```

### Method 2: Debian Package

```bash
sudo dpkg -i build/warpify_1.0.0_amd64.deb
```

### Method 3: AppImage

```bash
chmod +x build/warpify-1.0.0-x86_64.AppImage
./build/warpify-1.0.0-x86_64.AppImage
```

### Method 4: Manual

```bash
sudo cp build/warpify /usr/local/bin/
sudo cp Icon.png /usr/share/icons/hicolor/512x512/apps/warpify.png
sudo cp build/warpify.desktop /usr/share/applications/
sudo update-desktop-database
sudo gtk-update-icon-cache /usr/share/icons/hicolor/
```

## 🎯 Usage

Launch from terminal:

```bash
warpify
```

Launch from application menu:

- Search for "Warpify" in your desktop launcher

### Sending Files

1. Click "Send Files"
2. Select file
3. Choose destination device
4. Wait for transfer to complete

### Receiving Files

1. Keep Warpify running
2. Accept incoming transfers
3. Files saved with verification

## 🔧 Build Options

### Development Build

```bash
make build
./build/warpify
```

### Static Binary

```bash
make build-static
```

### Fyne Packager Build

```bash
make build-fyne
```

### AppImage

```bash
make appimage
```

### Debian Package

```bash
make deb
```

### Full Release

```bash
make release
```

## 📁 Project Structure

```
warpify/
├── main.go
├── Makefile
├── build.sh
├── FyneApp.toml
├── Icon.png
├── warpify.desktop
├── tools/
│   └── generate_icon.go
├── build/
└── README.md
```

## 🐛 Troubleshooting

**Dependencies not found:**

```bash
go get fyne.io/fyne/v2
go get github.com/grandcat/zeroconf
go mod tidy
```

**Icon issues:**

```bash
go run tools/generate_icon.go
sudo gtk-update-icon-cache /usr/share/icons/hicolor/
```

**No devices detected:**

- Ensure same network
- Open port 42424 in firewall
- mDNS/Avahi service running

```bash
sudo systemctl status avahi-daemon
```

**Firewall example (Ubuntu):**

```bash
sudo ufw allow 42424/tcp
```

## 🔒 Security Features

- SHA-256 checksums
- Input validation
- Path traversal protection
- File size limits (10GB max)
- Connection timeouts
- Protocol version checking

## 🎨 Customization

- Replace `Icon.png` for a custom icon
- Change `advertisePort` in `main.go`

## 🤝 Contributing

Open a Pull Request on GitHub

## 📄 License

MIT License

---

**Made with ❤️ for the open source community**
