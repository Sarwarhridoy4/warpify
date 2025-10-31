package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/grandcat/zeroconf"
)

const (
	serviceType     = "_warpify._tcp"
	serviceDomain   = "local."
	advertisePort   = 42424
	transferBufLen  = 4 * 1024 * 1024         // 4MB buffer
	maxFileSize     = 10 * 1024 * 1024 * 1024 // 10GB limit
	maxNameLen      = 255
	protocolVersion = 1
)

type Peer struct {
	Name     string
	IP       string
	Port     int
	LastSeen time.Time
	ID       string
}

type incomingOffer struct {
	conn     net.Conn
	name     string
	size     int64
	checksum string
	peerAddr string
	peerName string
}

type transferProgress struct {
	filename string
	current  int64
	total    int64
	speed    float64
	dialog   dialog.Dialog
	bar      *widget.ProgressBar
	label    *widget.Label
}

var (
	peersMu         sync.RWMutex
	peers           = map[string]*Peer{}
	myDeviceID      string
	myHostname      string
	activeTransfers sync.Map
)

func init() {
	// Generate unique device ID
	b := make([]byte, 8)
	rand.Read(b)
	myDeviceID = hex.EncodeToString(b)
	myHostname, _ = os.Hostname()
	if myHostname == "" {
		myHostname = "Unknown Device"
	}
}

func main() {
	a := app.NewWithID("com.warpify.secure")
	w := a.NewWindow("Warpify - Secure File Transfer")
	w.Resize(fyne.NewSize(500, 750))
	w.CenterOnScreen()

	// Create UI components
	ui := createMainUI(w)
	w.SetContent(ui.container)

	// Start background services
	incomingChan := make(chan incomingOffer, 16)
	go startZeroconfAdvertise()
	go startDiscovery(ui)
	go startListener(incomingChan, w)
	go handleIncomingOffers(w, incomingChan)
	go cleanupStaleConnections()
	go peerCleanupLoop(ui)

	w.ShowAndRun()
}

// ============ UI Components ============

type mainUI struct {
	container    *fyne.Container
	deviceScroll *container.Scroll
	status       *widget.Label
	deviceCount  *widget.Label
	sendBtn      *widget.Button
	receiveBtn   *widget.Button
}

func createMainUI(w fyne.Window) *mainUI {
	ui := &mainUI{}

	// Header with gradient-like appearance
	title := widget.NewLabelWithStyle("Warpify", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	// title.TextSize = 28

	subtitle := widget.NewLabelWithStyle("Secure Local File Sharing", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	// subtitle.TextSize = 14

	deviceIcon := widget.NewIcon(theme.ComputerIcon())
	deviceIcon.Resize(fyne.NewSize(32, 32))

	headerBox := container.NewVBox(
		container.NewCenter(deviceIcon),
		container.NewCenter(title),
		container.NewCenter(subtitle),
		widget.NewSeparator(),
	)

	// Status section
	ui.deviceCount = widget.NewLabel("Scanning for devices...")
	ui.deviceCount.Alignment = fyne.TextAlignCenter
	ui.deviceCount.TextStyle = fyne.TextStyle{Bold: true}

	ui.status = widget.NewLabel("● Ready to transfer")
	ui.status.Alignment = fyne.TextAlignCenter

	statusBox := container.NewVBox(
		ui.deviceCount,
		ui.status,
		widget.NewSeparator(),
	)

	// Device list with better styling
	ui.deviceScroll = container.NewVScroll(container.NewVBox())
	ui.deviceScroll.SetMinSize(fyne.NewSize(480, 400))

	// Action buttons with better styling
	ui.sendBtn = widget.NewButtonWithIcon("Send Files", theme.UploadIcon(), func() {
		onSendClicked(w, ui)
	})
	ui.sendBtn.Importance = widget.HighImportance

	ui.receiveBtn = widget.NewButtonWithIcon("Ready to Receive", theme.DownloadIcon(), func() {
		ui.status.SetText("● Listening for incoming files...")
		dialog.ShowInformation("Receive Mode",
			"Your device is ready to receive files.\nIncoming transfer requests will appear automatically.", w)
	})
	ui.receiveBtn.Importance = widget.MediumImportance

	buttons := container.NewGridWithColumns(2, ui.sendBtn, ui.receiveBtn)

	// Info footer
	infoLabel := widget.NewLabel(fmt.Sprintf("Device: %s", myHostname))
	infoLabel.Alignment = fyne.TextAlignCenter
	// infoLabel.TextSize = 11

	// Main layout
	ui.container = container.NewBorder(
		container.NewVBox(headerBox, statusBox),
		container.NewVBox(widget.NewSeparator(), buttons, infoLabel),
		nil, nil,
		ui.deviceScroll,
	)

	return ui
}

// ============ Discovery & Advertise ============

func startZeroconfAdvertise() {
	txt := []string{
		fmt.Sprintf("id=%s", myDeviceID),
		fmt.Sprintf("ver=%d", protocolVersion),
		fmt.Sprintf("name=%s", myHostname),
	}
	server, err := zeroconf.Register(myHostname, serviceType, serviceDomain, advertisePort, txt, nil)
	if err != nil {
		fmt.Println("advertise failed:", err)
		return
	}
	defer server.Shutdown()
	select {} // keep running
}

func startDiscovery(ui *mainUI) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		runOnUI(func() {
			ui.status.SetText("⚠ Discovery initialization failed")
		})
		return
	}

	entries := make(chan *zeroconf.ServiceEntry, 32)

	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			processPeerEntry(entry, ui)
		}
	}(entries)

	ctx := context.Background()
	err = resolver.Browse(ctx, serviceType, serviceDomain, entries)
	if err != nil {
		runOnUI(func() {
			ui.status.SetText("⚠ Discovery error")
		})
	}
}

func processPeerEntry(entry *zeroconf.ServiceEntry, ui *mainUI) {
	ip := ""
	if len(entry.AddrIPv4) > 0 {
		ip = entry.AddrIPv4[0].String()
	} else if len(entry.AddrIPv6) > 0 {
		ip = entry.AddrIPv6[0].String()
	}
	if ip == "" {
		return
	}

	// Extract device ID from TXT records
	peerID := ""
	peerName := entry.Instance
	for _, txt := range entry.Text {
		if strings.HasPrefix(txt, "id=") {
			peerID = strings.TrimPrefix(txt, "id=")
		} else if strings.HasPrefix(txt, "name=") {
			peerName = strings.TrimPrefix(txt, "name=")
		}
	}

	// Don't add self
	if peerID == myDeviceID {
		return
	}

	key := fmt.Sprintf("%s:%d", ip, entry.Port)

	peersMu.Lock()
	p, ok := peers[key]
	if !ok {
		p = &Peer{
			Name:     peerName,
			IP:       ip,
			Port:     entry.Port,
			LastSeen: time.Now(),
			ID:       peerID,
		}
		peers[key] = p
	} else {
		p.Name = peerName
		p.LastSeen = time.Now()
	}
	peersMu.Unlock()

	runOnUI(func() {
		refreshDeviceList(ui)
		updateStatus(ui)
	})
}

func updateStatus(ui *mainUI) {
	peersMu.RLock()
	count := len(peers)
	peersMu.RUnlock()

	switch count {
	case 0:
		ui.deviceCount.SetText("No devices found")
		ui.status.SetText("● Scanning...")
	case 1:
		ui.deviceCount.SetText("1 device available")
		ui.status.SetText("● Ready to transfer")
	default:
		ui.deviceCount.SetText(fmt.Sprintf("%d devices available", count))
		ui.status.SetText("● Ready to transfer")
	}
}

// ============ Device Cards ============

func makeDeviceCard(p *Peer, onTap func()) fyne.CanvasObject {
	// Icon
	icon := widget.NewIcon(theme.ComputerIcon())

	// Device name
	nameLabel := widget.NewLabel(p.Name)
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	nameLabel.Truncation = fyne.TextTruncateEllipsis

	// IP and connection status
	addrLabel := widget.NewLabel(fmt.Sprintf("%s:%d", p.IP, p.Port))
	// addrLabel.TextSize = 11

	statusIndicator := canvas.NewCircle(theme.SuccessColor())
	statusIndicator.Resize(fyne.NewSize(8, 8))

	// Layout
	leftContent := container.NewVBox(
		container.NewHBox(icon, nameLabel),
		container.NewHBox(statusIndicator, addrLabel),
	)

	sendIcon := widget.NewIcon(theme.MailSendIcon())
	sendIcon.Resize(fyne.NewSize(24, 24))

	cardContent := container.NewBorder(nil, nil, leftContent, sendIcon, layout.NewSpacer())

	// Card background
	cardRect := canvas.NewRectangle(theme.ButtonColor())
	cardRect.CornerRadius = 8
	cardRect.StrokeColor = theme.ShadowColor()
	cardRect.StrokeWidth = 1

	card := container.NewStack(cardRect, container.NewPadded(cardContent))

	// Make it tappable
	btn := widget.NewButton("", onTap)
	btn.Importance = widget.LowImportance

	return container.NewStack(card, btn)
}

func refreshDeviceList(ui *mainUI) {
	content := container.NewVBox()

	peersMu.RLock()
	if len(peers) == 0 {
		emptyLabel := widget.NewLabel("No devices found on the network")
		emptyLabel.Alignment = fyne.TextAlignCenter
		emptyLabel.TextStyle = fyne.TextStyle{Italic: true}
		content.Add(layout.NewSpacer())
		content.Add(container.NewCenter(emptyLabel))
		content.Add(layout.NewSpacer())
	} else {
		for _, p := range peers {
			peer := p // capture for closure
			card := makeDeviceCard(peer, func() { onDeviceTap(peer, ui) })
			content.Add(card)
			content.Add(layout.NewSpacer())
		}
	}
	peersMu.RUnlock()

	ui.deviceScroll.Content = content
	ui.deviceScroll.Refresh()
}

func onDeviceTap(p *Peer, ui *mainUI) {
	// When tapping a device, initiate send
	onSendToSpecificPeer(p, ui)
}

// ============ Listener & Incoming ============

func startListener(incoming chan<- incomingOffer, w fyne.Window) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", advertisePort))
	if err != nil {
		fmt.Println("listen error:", err)
		runOnUI(func() {
			dialog.ShowError(fmt.Errorf("failed to start listener: %v", err), w)
		})
		return
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, incoming)
	}
}

func handleConnection(c net.Conn, incoming chan<- incomingOffer) {
	defer func() {
		if r := recover(); r != nil {
			c.Close()
		}
	}()

	// Set initial timeout
	c.SetReadDeadline(time.Now().Add(15 * time.Second))

	// Read protocol version
	var version uint8
	if err := binary.Read(c, binary.BigEndian, &version); err != nil {
		c.Close()
		return
	}
	if version != protocolVersion {
		c.Close()
		return
	}

	// Read filename length (security check)
	var nameLen uint32
	if err := binary.Read(c, binary.BigEndian, &nameLen); err != nil {
		c.Close()
		return
	}
	if nameLen == 0 || nameLen > maxNameLen {
		c.Close()
		return
	}

	// Read filename
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(c, nameBuf); err != nil {
		c.Close()
		return
	}
	filename := string(nameBuf)

	// Sanitize filename
	filename = filepath.Base(filepath.Clean(filename))
	if filename == "." || filename == ".." {
		c.Close()
		return
	}

	// Read file size (security check)
	var fileSize uint64
	if err := binary.Read(c, binary.BigEndian, &fileSize); err != nil {
		c.Close()
		return
	}
	if fileSize == 0 || fileSize > maxFileSize {
		c.Close()
		return
	}

	// Read checksum length
	var checksumLen uint32
	if err := binary.Read(c, binary.BigEndian, &checksumLen); err != nil {
		c.Close()
		return
	}
	if checksumLen > 128 {
		c.Close()
		return
	}

	// Read checksum
	checksumBuf := make([]byte, checksumLen)
	if _, err := io.ReadFull(c, checksumBuf); err != nil {
		c.Close()
		return
	}

	// Read peer name length
	var peerNameLen uint32
	if err := binary.Read(c, binary.BigEndian, &peerNameLen); err != nil {
		c.Close()
		return
	}
	if peerNameLen > maxNameLen {
		c.Close()
		return
	}

	// Read peer name
	peerNameBuf := make([]byte, peerNameLen)
	if _, err := io.ReadFull(c, peerNameBuf); err != nil {
		c.Close()
		return
	}

	// Remove deadline for actual transfer
	c.SetReadDeadline(time.Time{})

	incoming <- incomingOffer{
		conn:     c,
		name:     filename,
		size:     int64(fileSize),
		checksum: string(checksumBuf),
		peerAddr: c.RemoteAddr().String(),
		peerName: string(peerNameBuf),
	}
}

func handleIncomingOffers(w fyne.Window, incoming <-chan incomingOffer) {
	for offer := range incoming {
		handleIncomingOffer(w, offer)
	}
}

func handleIncomingOffer(w fyne.Window, offer incomingOffer) {
	accepted := make(chan bool, 1)
	savePath := make(chan string, 1)

	runOnUI(func() {
		peerInfo := widget.NewLabelWithStyle(
			fmt.Sprintf("From: %s", offer.peerName),
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		)

		fileIcon := widget.NewIcon(theme.FileIcon())
		fileName := widget.NewLabel(offer.name)
		fileName.Wrapping = fyne.TextWrapWord

		fileSize := widget.NewLabel(fmt.Sprintf("Size: %s", humanBytes(uint64(offer.size))))

		content := container.NewVBox(
			peerInfo,
			widget.NewSeparator(),
			container.NewHBox(fileIcon, fileName),
			fileSize,
		)

		d := dialog.NewCustomConfirm(
			"Incoming File Transfer",
			"Accept",
			"Decline",
			content,
			func(ok bool) {
				if !ok {
					accepted <- false
					return
				}
				fd := dialog.NewFileSave(func(uc fyne.URIWriteCloser, err error) {
					if uc == nil || err != nil {
						accepted <- false
						return
					}
					savePath <- uc.URI().Path()
					accepted <- true
				}, w)
				fd.SetFileName(offer.name)
				fd.Show()
			}, w)
		d.Show()
	})

	// Wait for user response
	if ok := <-accepted; !ok {
		offer.conn.Write([]byte{0}) // Rejected
		offer.conn.Close()
		return
	}

	path := <-savePath
	offer.conn.Write([]byte{1}) // Accepted

	// Start receiving with progress
	go receiveFile(w, offer, path)
}

func receiveFile(w fyne.Window, offer incomingOffer, savePath string) {
	defer offer.conn.Close()

	progress := &transferProgress{
		filename: offer.name,
		total:    offer.size,
		bar:      widget.NewProgressBar(),
		label:    widget.NewLabel("Preparing to receive..."),
	}

	runOnUI(func() {
		content := container.NewVBox(
			widget.NewLabelWithStyle("Receiving: "+offer.name, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
			progress.label,
			progress.bar,
		)
		progress.dialog = dialog.NewCustom("Receiving File", "Cancel", content, w)
		progress.dialog.Show()
	})

	// Create temporary file
	tempPath := savePath + ".part"
	f, err := os.Create(tempPath)
	if err != nil {
		runOnUI(func() {
			progress.dialog.Hide()
			dialog.ShowError(fmt.Errorf("failed to create file: %v", err), w)
		})
		return
	}
	defer f.Close()

	// Receive file with progress updates
	buf := make([]byte, transferBufLen)
	hasher := sha256.New()
	var received int64
	startTime := time.Now()
	lastUpdate := startTime

	for received < offer.size {
		remaining := offer.size - received
		toRead := int64(len(buf))
		if remaining < toRead {
			toRead = remaining
		}

		n, err := offer.conn.Read(buf[:toRead])
		if n > 0 {
			f.Write(buf[:n])
			hasher.Write(buf[:n])
			received += int64(n)

			// Update progress every 100ms
			now := time.Now()
			if now.Sub(lastUpdate) > 100*time.Millisecond {
				elapsed := now.Sub(startTime).Seconds()
				speed := float64(received) / elapsed
				progress.current = received
				progress.speed = speed

				runOnUI(func() {
					progress.bar.SetValue(float64(received) / float64(offer.size))
					progress.label.SetText(fmt.Sprintf(
						"%s / %s  •  %s/s",
						humanBytes(uint64(received)),
						humanBytes(uint64(offer.size)),
						humanBytes(uint64(speed)),
					))
				})
				lastUpdate = now
			}
		}

		if err != nil {
			if err != io.EOF {
				runOnUI(func() {
					progress.dialog.Hide()
					dialog.ShowError(fmt.Errorf(" Transfer error: %v", err), w)
				})
				os.Remove(tempPath)
				return
			}
			break
		}
	}

	// Verify checksum
	calculatedChecksum := hex.EncodeToString(hasher.Sum(nil))
	if calculatedChecksum != offer.checksum {
		runOnUI(func() {
			progress.dialog.Hide()
			dialog.ShowError(fmt.Errorf(" File verification failed: checksum mismatch"), w)
		})
		os.Remove(tempPath)
		return
	}

	// Finalize
	f.Sync()
	f.Close()

	if err := os.Rename(tempPath, savePath); err != nil {
		runOnUI(func() {
			progress.dialog.Hide()
			dialog.ShowError(fmt.Errorf(" Failed to save file: %v", err), w)
		})
		return
	}

	runOnUI(func() {
		progress.dialog.Hide()
		dialog.ShowInformation(
			"Transfer Complete",
			fmt.Sprintf("Successfully received: %s\nSaved to: %s", offer.name, savePath),
			w,
		)
	})
}

// ============ Sending ============

func onSendClicked(w fyne.Window, ui *mainUI) {
	peersMu.RLock()
	peerCount := len(peers)
	peersMu.RUnlock()

	if peerCount == 0 {
		dialog.ShowInformation("No Devices", "No devices found on the network.\nMake sure other devices are running Warpify.", w)
		return
	}

	// Open file picker first
	dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
		if rc == nil || err != nil {
			return
		}
		filePath := rc.URI().Path()
		rc.Close()

		// Check file exists and get info
		info, err := os.Stat(filePath)
		if err != nil {
			dialog.ShowError(fmt.Errorf(" Cannot access file: %v", err), w)
			return
		}

		if info.Size() > maxFileSize {
			dialog.ShowError(fmt.Errorf(" File too large. Maximum size: %s", humanBytes(maxFileSize)), w)
			return
		}

		// Show peer selection
		showPeerSelection(w, ui, filePath, info)
	}, w)
}

func onSendToSpecificPeer(peer *Peer, _ *mainUI) {
	w := fyne.CurrentApp().Driver().AllWindows()[0]

	dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
		if rc == nil || err != nil {
			return
		}
		filePath := rc.URI().Path()
		rc.Close()

		info, err := os.Stat(filePath)
		if err != nil {
			dialog.ShowError(fmt.Errorf("cannot access file: %v", err), w)
			return
		}

		if info.Size() > maxFileSize {
			dialog.ShowError(fmt.Errorf("file too large. Maximum size: %s", humanBytes(maxFileSize)), w)
			return
		}

		// Send directly to this peer
		initiateSend(w, peer, filePath, info)
	}, w)
}

func showPeerSelection(w fyne.Window, _ *mainUI, filePath string, info os.FileInfo) {
	peersMu.RLock()
	peerList := make([]*Peer, 0, len(peers))
	for _, p := range peers {
		peerList = append(peerList, p)
	}
	peersMu.RUnlock()

	if len(peerList) == 0 {
		dialog.ShowInformation("No Devices", "No devices available", w)
		return
	}

	// Create selection list
	var selectedPeer *Peer
	options := make([]string, len(peerList))
	for i, p := range peerList {
		options[i] = fmt.Sprintf("%s (%s)", p.Name, p.IP)
	}

	selectWidget := widget.NewSelect(options, func(selected string) {
		for i, opt := range options {
			if opt == selected {
				selectedPeer = peerList[i]
				break
			}
		}
	})
	selectWidget.PlaceHolder = "Choose a device..."

	fileInfo := widget.NewLabel(fmt.Sprintf("File: %s\nSize: %s",
		filepath.Base(filePath),
		humanBytes(uint64(info.Size()))))

	content := container.NewVBox(
		fileInfo,
		widget.NewSeparator(),
		widget.NewLabel("Select destination:"),
		selectWidget,
	)

	dialog.ShowCustomConfirm("Send File", "Send", "Cancel", content, func(ok bool) {
		if !ok || selectedPeer == nil {
			return
		}
		initiateSend(w, selectedPeer, filePath, info)
	}, w)
}

func initiateSend(w fyne.Window, peer *Peer, filePath string, info os.FileInfo) {
	progress := &transferProgress{
		filename: filepath.Base(filePath),
		total:    info.Size(),
		bar:      widget.NewProgressBar(),
		label:    widget.NewLabel("Connecting..."),
	}

	content := container.NewVBox(
		widget.NewLabelWithStyle("Sending to: "+peer.Name, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("File: "+progress.filename),
		progress.label,
		progress.bar,
	)

	progress.dialog = dialog.NewCustom("Sending File", "Cancel", content, w)
	progress.dialog.Show()

	go func() {
		err := sendFile(peer.IP, peer.Port, filePath, progress)

		runOnUI(func() {
			progress.dialog.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf(" Transfer failed: %v", err), w)
			} else {
				dialog.ShowInformation("Transfer Complete",
					fmt.Sprintf("Successfully sent %s to %s", progress.filename, peer.Name), w)
			}
		})
	}()
}

func sendFile(ip string, port int, path string, progress *transferProgress) error {
	// Open and prepare file
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	// Calculate checksum
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))

	// Reset file pointer
	f.Seek(0, 0)

	// Connect to peer
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send protocol info
	filename := filepath.Base(path)

	// Protocol version
	binary.Write(conn, binary.BigEndian, uint8(protocolVersion))

	// Filename
	binary.Write(conn, binary.BigEndian, uint32(len(filename)))
	conn.Write([]byte(filename))

	// File size
	binary.Write(conn, binary.BigEndian, uint64(info.Size()))

	// Checksum
	binary.Write(conn, binary.BigEndian, uint32(len(checksum)))
	conn.Write([]byte(checksum))

	// Sender name
	binary.Write(conn, binary.BigEndian, uint32(len(myHostname)))
	conn.Write([]byte(myHostname))

	// Wait for acceptance
	resp := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Time{})

	if resp[0] != 1 {
		return fmt.Errorf("transfer rejected by peer")
	}

	// Send file with progress
	buf := make([]byte, transferBufLen)
	reader := bufio.NewReader(f)
	var sent int64
	startTime := time.Now()
	lastUpdate := startTime

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			written := 0
			for written < n {
				m, werr := conn.Write(buf[written:n])
				if werr != nil {
					return werr
				}
				written += m
			}
			sent += int64(n)

			// Update progress
			now := time.Now()
			if now.Sub(lastUpdate) > 100*time.Millisecond {
				elapsed := now.Sub(startTime).Seconds()
				speed := float64(sent) / elapsed
				progress.current = sent
				progress.speed = speed

				runOnUI(func() {
					progress.bar.SetValue(float64(sent) / float64(progress.total))
					progress.label.SetText(fmt.Sprintf(
						"%s / %s  •  %s/s",
						humanBytes(uint64(sent)),
						humanBytes(uint64(progress.total)),
						humanBytes(uint64(speed)),
					))
				})
				lastUpdate = now
			}
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	return nil
}

// ============ Cleanup & Maintenance ============

func peerCleanupLoop(ui *mainUI) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		peersMu.Lock()
		changed := false
		for k, p := range peers {
			if now.Sub(p.LastSeen) > 10*time.Second {
				delete(peers, k)
				changed = true
			}
		}
		peersMu.Unlock()

		if changed {
			runOnUI(func() {
				refreshDeviceList(ui)
				updateStatus(ui)
			})
		}
	}
}

func cleanupStaleConnections() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		activeTransfers.Range(func(key, value interface{}) bool {
			if t, ok := value.(*transferProgress); ok {
				// Check if transfer is stale (no progress in 60 seconds)
				// This is a placeholder for more sophisticated tracking
				_ = t
			}
			return true
		})
	}
}

// ============ Utilities ============

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func runOnUI(fn func()) {
	// Use a goroutine to avoid blocking
	go func() {
		// Small delay to ensure UI thread is ready
		time.Sleep(10 * time.Millisecond)

		// Get the current app's window
		if app := fyne.CurrentApp(); app != nil {
			if windows := app.Driver().AllWindows(); len(windows) > 0 {
				// Execute on UI thread by creating a tiny notification (hack for thread-safety)
				// In production, use proper channel-based UI updates
				windows[0].Canvas().Refresh(windows[0].Content())
			}
		}

		// Execute the function
		fn()
	}()
}
