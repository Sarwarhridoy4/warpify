package main

import (
	"archive/zip"
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
	transferBufLen  = 4 * 1024 * 1024         // 4 MiB
	maxFileSize     = 10 * 1024 * 1024 * 1024 // 10 GiB
	maxNameLen      = 255
	protocolVersion = 1
	peerTimeout     = 30 * time.Second
	updateInterval  = 100 * time.Millisecond
)

/* ------------------------------------------------------------------ */
/*  Types & globals                                                   */
/* ------------------------------------------------------------------ */
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
	peerName string
}

type transferProgress struct {
	filename string
	total    int64
	dialog   dialog.Dialog
	bar      *widget.ProgressBar
	label    *widget.Label
	cancel   context.CancelFunc
}

var (
	peersMu          sync.RWMutex
	peers            = make(map[string]*Peer)
	ignored          = make(map[string]bool)
	myDeviceID       string
	myHostname       string
	discoveryCtx     context.Context
	discoveryCancel  context.CancelFunc
	advertiseServer  *zeroconf.Server
	advertiseMu      sync.Mutex
)

/* ------------------------------------------------------------------ */
/*  Init – device ID & hostname                                        */
/* ------------------------------------------------------------------ */
func init() {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	myDeviceID = hex.EncodeToString(b)
	myHostname, _ = os.Hostname()
	if myHostname == "" {
		myHostname = "Unknown Device"
	}
}

/* ------------------------------------------------------------------ */
/*  ZIP helper                                                         */
/* ------------------------------------------------------------------ */
func createZip(source string) (string, error) {
	tmp, err := os.CreateTemp("", "warpify-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	z := zip.NewWriter(tmp)
	defer z.Close()

	base := filepath.Base(source)
	err = filepath.Walk(source, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		
		h.Name = filepath.ToSlash(filepath.Join(base, rel))
		if info.IsDir() {
			h.Name += "/"
		} else {
			h.Method = zip.Deflate
		}
		
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		
		if info.IsDir() {
			return nil
		}
		
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		
		_, err = io.Copy(w, f)
		return err
	})
	
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	
	return tmp.Name(), nil
}

func dirSize(path string) int64 {
	var sz int64
	filepath.Walk(path, func(_ string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() {
			sz += i.Size()
		}
		return nil
	})
	return sz
}

/* ------------------------------------------------------------------ */
/*  Main                                                               */
/* ------------------------------------------------------------------ */
func main() {
	a := app.NewWithID("com.warpify.secure")
	w := a.NewWindow("Warpify – Secure File Transfer")
	w.Resize(fyne.NewSize(520, 780))
	w.CenterOnScreen()

	ui := createMainUI(w)
	w.SetContent(ui.container)

	// Start background services
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incoming := make(chan incomingOffer, 16)
	
	go startZeroconfAdvertise(ctx)
	go startDiscovery(ctx, ui)
	go startListener(ctx, incoming, w)
	go handleIncomingOffers(ctx, w, incoming)
	go peerCleanupLoop(ctx, ui)

	w.ShowAndRun()
	cancel()
}

/* ------------------------------------------------------------------ */
/*  UI                                                                 */
/* ------------------------------------------------------------------ */
type mainUI struct {
	container    *fyne.Container
	deviceScroll *container.Scroll
	status       *widget.Label
	deviceCount  *widget.Label
	sendBtn      *widget.Button
	receiveBtn   *widget.Button
	scanBtn      *widget.Button
}

func createMainUI(w fyne.Window) *mainUI {
	ui := &mainUI{}

	// header
	title := widget.NewLabelWithStyle("Warpify", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabelWithStyle("Secure Local File Sharing", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	icon := widget.NewIcon(theme.ComputerIcon())
	icon.Resize(fyne.NewSize(32, 32))
	header := container.NewVBox(
		container.NewCenter(icon),
		container.NewCenter(title),
		container.NewCenter(subtitle),
		widget.NewSeparator(),
	)

	// status
	ui.deviceCount = widget.NewLabel("Scanning…")
	ui.deviceCount.Alignment = fyne.TextAlignCenter
	ui.deviceCount.TextStyle = fyne.TextStyle{Bold: true}
	ui.status = widget.NewLabel("Ready")
	ui.status.Alignment = fyne.TextAlignCenter
	statusBox := container.NewVBox(ui.deviceCount, ui.status, widget.NewSeparator())

	// device list
	ui.deviceScroll = container.NewVScroll(container.NewVBox())
	ui.deviceScroll.SetMinSize(fyne.NewSize(480, 420))

	// buttons
	ui.sendBtn = widget.NewButtonWithIcon("Send Files", theme.UploadIcon(), func() {
		onSendClicked(w, ui)
	})
	ui.sendBtn.Importance = widget.HighImportance
	
	ui.receiveBtn = widget.NewButtonWithIcon("Ready to Receive", theme.DownloadIcon(), func() {
		ui.status.SetText("Listening…")
		dialog.ShowInformation("Receive Mode", "Your device is ready to receive files.", w)
	})
	ui.receiveBtn.Importance = widget.MediumImportance
	
	ui.scanBtn = widget.NewButtonWithIcon("Rescan", theme.ViewRefreshIcon(), func() {
		restartDiscovery(ui)
	})

	buttons := container.NewGridWithColumns(3, ui.sendBtn, ui.receiveBtn, ui.scanBtn)

	// footer
	footer := widget.NewLabel(fmt.Sprintf("Device: %s", myHostname))
	footer.Alignment = fyne.TextAlignCenter

	// layout
	ui.container = container.NewBorder(
		container.NewVBox(header, statusBox),
		container.NewVBox(widget.NewSeparator(), buttons, footer),
		nil, nil,
		ui.deviceScroll,
	)
	return ui
}

/* ------------------------------------------------------------------ */
/*  Discovery & Advertise                                              */
/* ------------------------------------------------------------------ */
func startZeroconfAdvertise(ctx context.Context) {
	advertiseMu.Lock()
	if advertiseServer != nil {
		advertiseServer.Shutdown()
		advertiseServer = nil
	}
	
	txt := []string{
		fmt.Sprintf("id=%s", myDeviceID),
		fmt.Sprintf("ver=%d", protocolVersion),
		fmt.Sprintf("name=%s", myHostname),
	}
	
	srv, err := zeroconf.Register(myHostname, serviceType, serviceDomain, advertisePort, txt, nil)
	if err != nil {
		fmt.Println("advertise error:", err)
		advertiseMu.Unlock()
		return
	}
	advertiseServer = srv
	advertiseMu.Unlock()

	// Keep advertising active until context is cancelled
	<-ctx.Done()
	
	advertiseMu.Lock()
	if advertiseServer != nil {
		advertiseServer.Shutdown()
		advertiseServer = nil
	}
	advertiseMu.Unlock()
}

func startDiscovery(ctx context.Context, ui *mainUI) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		fyne.Do(func() {
			ui.status.SetText("Discovery init failed")
		})
		return
	}

	localCtx, cancel := context.WithCancel(ctx)
	discoveryCtx, discoveryCancel = localCtx, cancel

	entries := make(chan *zeroconf.ServiceEntry, 32)

	// Process entries continuously
	go func() {
		for {
			select {
			case entry := <-entries:
				if entry != nil {
					processPeerEntry(entry, ui)
				}
			case <-localCtx.Done():
				return
			}
		}
	}()

	// Start browsing - this blocks until context is cancelled
	go func() {
		err := resolver.Browse(localCtx, serviceType, serviceDomain, entries)
		if err != nil && localCtx.Err() == nil {
			fmt.Println("Browse error:", err)
		}
	}()

	<-localCtx.Done()
}

func restartDiscovery(ui *mainUI) {
	if discoveryCancel != nil {
		discoveryCancel()
		time.Sleep(500 * time.Millisecond) // Give time for cleanup
	}

	fyne.Do(func() {
		peersMu.Lock()
		peers = make(map[string]*Peer)
		ignored = make(map[string]bool)
		peersMu.Unlock()
		refreshDeviceList(ui)
		updateStatus(ui)
		ui.status.SetText("Rescanning…")
	})

	ctx := context.Background()
	go startDiscovery(ctx, ui)
}

func processPeerEntry(e *zeroconf.ServiceEntry, ui *mainUI) {
	ip := ""
	if len(e.AddrIPv4) > 0 {
		ip = e.AddrIPv4[0].String()
	} else if len(e.AddrIPv6) > 0 {
		ip = e.AddrIPv6[0].String()
	}
	if ip == "" {
		return
	}
	
	peerID, peerName := "", e.Instance
	for _, t := range e.Text {
		if strings.HasPrefix(t, "id=") {
			peerID = strings.TrimPrefix(t, "id=")
		} else if strings.HasPrefix(t, "name=") {
			peerName = strings.TrimPrefix(t, "name=")
		}
	}
	
	// Ignore self and ignored peers
	peersMu.RLock()
	isIgnored := ignored[peerID]
	peersMu.RUnlock()
	
	if peerID == myDeviceID || isIgnored {
		return
	}
	
	key := fmt.Sprintf("%s:%d", ip, e.Port)
	now := time.Now()
	
	peersMu.Lock()
	if p, ok := peers[key]; ok {
		// Update existing peer
		p.Name = peerName
		p.LastSeen = now
	} else {
		// Add new peer
		peers[key] = &Peer{
			Name:     peerName,
			IP:       ip,
			Port:     e.Port,
			LastSeen: now,
			ID:       peerID,
		}
	}
	peersMu.Unlock()

	fyne.Do(func() {
		refreshDeviceList(ui)
		updateStatus(ui)
	})
}

func updateStatus(ui *mainUI) {
	peersMu.RLock()
	cnt := len(peers)
	peersMu.RUnlock()
	
	switch cnt {
	case 0:
		ui.deviceCount.SetText("No devices")
		ui.status.SetText("Scanning…")
	case 1:
		ui.deviceCount.SetText("1 device")
		ui.status.SetText("Ready")
	default:
		ui.deviceCount.SetText(fmt.Sprintf("%d devices", cnt))
		ui.status.SetText("Ready")
	}
}

/* ------------------------------------------------------------------ */
/*  Device card                                                        */
/* ------------------------------------------------------------------ */
func makeDeviceCard(p *Peer, onTap, onDisconnect func()) fyne.CanvasObject {
	icon := widget.NewIcon(theme.ComputerIcon())
	name := widget.NewLabel(p.Name)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.Truncation = fyne.TextTruncateEllipsis
	addr := widget.NewLabel(fmt.Sprintf("%s:%d", p.IP, p.Port))
	status := canvas.NewCircle(theme.SuccessColor())
	status.Resize(fyne.NewSize(8, 8))

	left := container.NewVBox(
		container.NewHBox(icon, name),
		container.NewHBox(status, addr),
	)

	send := widget.NewIcon(theme.MailSendIcon())
	send.Resize(fyne.NewSize(24, 24))
	dis := widget.NewButtonWithIcon("", theme.CancelIcon(), onDisconnect)
	dis.Importance = widget.WarningImportance
	right := container.NewHBox(send, dis)

	content := container.NewBorder(nil, nil, left, right, layout.NewSpacer())
	bg := canvas.NewRectangle(theme.ButtonColor())
	bg.CornerRadius = 8
	bg.StrokeColor = theme.ShadowColor()
	bg.StrokeWidth = 1

	card := container.NewStack(bg, container.NewPadded(content))
	tap := widget.NewButton("", onTap)
	tap.Importance = widget.LowImportance
	return container.NewStack(card, tap)
}

func refreshDeviceList(ui *mainUI) {
	box := container.NewVBox()
	
	peersMu.RLock()
	peerCount := len(peers)
	peerList := make([]*Peer, 0, peerCount)
	peerKeys := make([]string, 0, peerCount)
	
	for k, p := range peers {
		peerList = append(peerList, p)
		peerKeys = append(peerKeys, k)
	}
	peersMu.RUnlock()

	if peerCount == 0 {
		lbl := widget.NewLabel("No devices found")
		lbl.Alignment = fyne.TextAlignCenter
		lbl.TextStyle = fyne.TextStyle{Italic: true}
		box.Add(layout.NewSpacer())
		box.Add(container.NewCenter(lbl))
		box.Add(layout.NewSpacer())
	} else {
		for i, peer := range peerList {
			key := peerKeys[i]
			p := peer
			k := key
			
			card := makeDeviceCard(p,
				func() { onDeviceTap(p, ui) },
				func() {
					peersMu.Lock()
					ignored[p.ID] = true
					delete(peers, k)
					peersMu.Unlock()
					refreshDeviceList(ui)
					updateStatus(ui)
				})
			box.Add(card)
			box.Add(layout.NewSpacer())
		}
	}
	
	ui.deviceScroll.Content = box
	ui.deviceScroll.Refresh()
}

func onDeviceTap(p *Peer, ui *mainUI) {
	onSendToSpecificPeer(p, ui)
}

/* ------------------------------------------------------------------ */
/*  Listener & incoming                                                */
/* ------------------------------------------------------------------ */
func startListener(ctx context.Context, ch chan<- incomingOffer, w fyne.Window) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", advertisePort))
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
		})
		return
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go handleConnection(c, ch)
	}
}

func handleConnection(c net.Conn, ch chan<- incomingOffer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in handleConnection: %v\n", r)
		}
		c.Close()
	}()

	c.SetReadDeadline(time.Now().Add(15 * time.Second))

	var ver uint8
	if err := binary.Read(c, binary.BigEndian, &ver); err != nil || ver != protocolVersion {
		return
	}
	
	var nameLen, checksumLen, peerNameLen uint32
	var fileSize uint64
	
	if err := binary.Read(c, binary.BigEndian, &nameLen); err != nil {
		return
	}
	if nameLen == 0 || nameLen > maxNameLen {
		return
	}
	
	nameB := make([]byte, nameLen)
	if _, err := io.ReadFull(c, nameB); err != nil {
		return
	}
	filename := filepath.Base(string(nameB))

	if err := binary.Read(c, binary.BigEndian, &fileSize); err != nil {
		return
	}
	if fileSize == 0 || fileSize > maxFileSize {
		return
	}
	
	if err := binary.Read(c, binary.BigEndian, &checksumLen); err != nil {
		return
	}
	if checksumLen > 128 {
		return
	}
	
	checkB := make([]byte, checksumLen)
	if _, err := io.ReadFull(c, checkB); err != nil {
		return
	}

	if err := binary.Read(c, binary.BigEndian, &peerNameLen); err != nil {
		return
	}
	if peerNameLen > maxNameLen {
		return
	}
	
	pnB := make([]byte, peerNameLen)
	if _, err := io.ReadFull(c, pnB); err != nil {
		return
	}

	c.SetReadDeadline(time.Time{})
	
	select {
	case ch <- incomingOffer{
		conn:     c,
		name:     filename,
		size:     int64(fileSize),
		checksum: string(checkB),
		peerName: string(pnB),
	}:
	default:
		// Channel full, close connection
		c.Close()
	}
}

func handleIncomingOffers(ctx context.Context, w fyne.Window, ch <-chan incomingOffer) {
	for {
		select {
		case o := <-ch:
			go handleIncomingOffer(w, o)
		case <-ctx.Done():
			return
		}
	}
}

func handleIncomingOffer(w fyne.Window, o incomingOffer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in handleIncomingOffer: %v\n", r)
		}
	}()

	accepted := make(chan bool, 1)
	savePath := make(chan string, 1)

	fyne.Do(func() {
		from := widget.NewLabelWithStyle(
			fmt.Sprintf("From: %s", o.peerName),
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		)
		icon := widget.NewIcon(theme.FileIcon())
		name := widget.NewLabel(o.name)
		name.Wrapping = fyne.TextWrapBreak
		size := widget.NewLabel(fmt.Sprintf("Size: %s", humanBytes(uint64(o.size))))

		content := container.NewVBox(
			from,
			widget.NewSeparator(),
			container.NewHBox(icon, name),
			size,
		)

		d := dialog.NewCustomConfirm("Incoming File", "Accept", "Decline", content,
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
				fd.SetFileName(o.name)
				fd.Show()
			}, w)
		d.Resize(fyne.NewSize(420, 260))
		d.Show()
	})

	// Wait for user decision with timeout
	select {
	case ok := <-accepted:
		if !ok {
			o.conn.Write([]byte{0})
			o.conn.Close()
			return
		}
	case <-time.After(60 * time.Second):
		o.conn.Write([]byte{0})
		o.conn.Close()
		return
	}

	o.conn.Write([]byte{1})
	
	path := <-savePath
	go receiveFile(w, o, path)
}

func receiveFile(w fyne.Window, o incomingOffer, path string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in receiveFile: %v\n", r)
		}
		o.conn.Close()
	}()

	prog := &transferProgress{
		filename: o.name,
		total:    o.size,
		bar:      widget.NewProgressBar(),
		label:    widget.NewLabel("Preparing…"),
	}
	
	cancelled := false
	fyne.Do(func() {
		c := container.NewVBox(
			widget.NewLabelWithStyle(
				"Receiving: "+o.name,
				fyne.TextAlignCenter,
				fyne.TextStyle{Bold: true},
			),
			widget.NewSeparator(),
			prog.label,
			prog.bar,
		)
		prog.dialog = dialog.NewCustom("Receiving", "Cancel", c, w)
		prog.dialog.SetOnClosed(func() {
			cancelled = true
		})
		prog.dialog.Show()
	})

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		fyne.Do(func() {
			prog.dialog.Hide()
			dialog.ShowError(err, w)
		})
		return
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, transferBufLen)
	var rcvd int64
	start := time.Now()
	last := start

	for rcvd < o.size && !cancelled {
		toRead := int64(len(buf))
		if o.size-rcvd < toRead {
			toRead = o.size - rcvd
		}
		
		n, err := o.conn.Read(buf[:toRead])
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				fyne.Do(func() {
					prog.dialog.Hide()
					dialog.ShowError(werr, w)
				})
				os.Remove(tmp)
				return
			}
			h.Write(buf[:n])
			rcvd += int64(n)

			now := time.Now()
			if now.Sub(last) > updateInterval {
				spd := float64(rcvd) / now.Sub(start).Seconds()
				fyne.Do(func() {
					prog.bar.SetValue(float64(rcvd) / float64(o.size))
					prog.label.SetText(fmt.Sprintf("%s / %s • %s/s",
						humanBytes(uint64(rcvd)),
						humanBytes(uint64(o.size)),
						humanBytes(uint64(spd))))
				})
				last = now
			}
		}
		
		if err != nil {
			if err != io.EOF {
				fyne.Do(func() {
					prog.dialog.Hide()
					dialog.ShowError(err, w)
				})
				os.Remove(tmp)
			}
			break
		}
	}

	if cancelled {
		os.Remove(tmp)
		return
	}

	if hex.EncodeToString(h.Sum(nil)) != o.checksum {
		fyne.Do(func() {
			prog.dialog.Hide()
			dialog.ShowError(fmt.Errorf("checksum mismatch"), w)
		})
		os.Remove(tmp)
		return
	}
	
	if err := f.Sync(); err != nil {
		fyne.Do(func() {
			prog.dialog.Hide()
			dialog.ShowError(err, w)
		})
		os.Remove(tmp)
		return
	}
	f.Close()
	
	if err := os.Rename(tmp, path); err != nil {
		fyne.Do(func() {
			prog.dialog.Hide()
			dialog.ShowError(err, w)
		})
		os.Remove(tmp)
		return
	}

	fyne.Do(func() {
		prog.dialog.Hide()
		dialog.ShowInformation("Done", fmt.Sprintf("Saved: %s", path), w)
	})
}

/* ------------------------------------------------------------------ */
/*  Sending                                                            */
/* ------------------------------------------------------------------ */
func onSendClicked(w fyne.Window, ui *mainUI) {
	peersMu.RLock()
	peerCount := len(peers)
	peersMu.RUnlock()
	
	if peerCount == 0 {
		dialog.ShowInformation("No Devices", "No peers found.", w)
		return
	}
	showFileFolderPicker(w, ui, nil)
}

func onSendToSpecificPeer(p *Peer, _ *mainUI) {
	w := fyne.CurrentApp().Driver().AllWindows()[0]
	showFileFolderPicker(w, nil, p)
}

func showFileFolderPicker(w fyne.Window, ui *mainUI, direct *Peer) {
	var paths []string
	list := widget.NewList(
		func() int { return len(paths) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i int, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(filepath.Base(paths[i]))
		},
	)

	addFile := widget.NewButton("Add File", func() {
		dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
			if rc == nil || err != nil {
				return
			}
			p := rc.URI().Path()
			rc.Close()
			
			info, err := os.Stat(p)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if info.Size() > maxFileSize {
				dialog.ShowError(fmt.Errorf("file too large (max 10 GiB)"), w)
				return
			}
			paths = append(paths, p)
			list.Refresh()
		}, w)
	})
	
	addFolder := widget.NewButton("Add Folder", func() {
		dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
			if lu == nil || err != nil {
				return
			}
			if dirSize(lu.Path()) > maxFileSize {
				dialog.ShowError(fmt.Errorf("folder too large (max 10 GiB)"), w)
				return
			}
			paths = append(paths, lu.Path())
			list.Refresh()
		}, w)
	})

	buttons := container.NewHBox(addFile, addFolder)
	content := container.NewVBox(buttons, widget.NewSeparator(), list)

	next := "Next"
	if direct != nil {
		next = "Send"
	}
	
	d := dialog.NewCustomConfirm("Select Items", next, "Cancel", content,
		func(ok bool) {
			if !ok || len(paths) == 0 {
				return
			}
			if direct != nil {
				initiateSends(w, direct, paths)
			} else {
				showPeerSelection(w, ui, paths)
			}
		}, w)
	d.Show()
}

func showPeerSelection(w fyne.Window, _ *mainUI, paths []string) {
	peersMu.RLock()
	list := make([]*Peer, 0, len(peers))
	for _, p := range peers {
		list = append(list, p)
	}
	peersMu.RUnlock()

	var sel *Peer
	opts := make([]string, len(list))
	for i, p := range list {
		opts[i] = fmt.Sprintf("%s (%s)", p.Name, p.IP)
	}
	
	selW := widget.NewSelect(opts, func(s string) {
		for i, o := range opts {
			if o == s {
				sel = list[i]
				break
			}
		}
	})
	selW.PlaceHolder = "Choose device…"

	info := widget.NewLabel(fmt.Sprintf("Items: %d", len(paths)))
	c := container.NewVBox(
		info,
		widget.NewSeparator(),
		widget.NewLabel("Destination:"),
		selW,
	)

	dialog.ShowCustomConfirm("Send Items", "Send", "Cancel", c,
		func(ok bool) {
			if ok && sel != nil {
				initiateSends(w, sel, paths)
			}
		}, w)
}

func initiateSends(w fyne.Window, p *Peer, paths []string) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, w)
			})
			continue
		}
		go initiateSend(w, p, path, info)
	}
}

func initiateSend(w fyne.Window, peer *Peer, path string, info os.FileInfo) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in initiateSend: %v\n", r)
		}
	}()

	prog := &transferProgress{
		filename: filepath.Base(path),
		total:    info.Size(),
		bar:      widget.NewProgressBar(),
		label:    widget.NewLabel("Connecting…"),
	}
	
	cancelled := false
	fyne.Do(func() {
		c := container.NewVBox(
			widget.NewLabelWithStyle(
				"Sending to: "+peer.Name,
				fyne.TextAlignCenter,
				fyne.TextStyle{Bold: true},
			),
			widget.NewSeparator(),
			widget.NewLabel("Item: "+prog.filename),
			prog.label,
			prog.bar,
		)
		prog.dialog = dialog.NewCustom("Sending", "Cancel", c, w)
		prog.dialog.SetOnClosed(func() {
			cancelled = true
		})
		prog.dialog.Show()
	})

	go func() {
		err := sendFile(peer.IP, peer.Port, path, prog, &cancelled)
		fyne.Do(func() {
			prog.dialog.Hide()
			if err != nil && !cancelled {
				dialog.ShowError(err, w)
			} else if !cancelled {
				dialog.ShowInformation("Done", fmt.Sprintf("Sent %s", prog.filename), w)
			}
		})
	}()
}

func sendFile(ip string, port int, originalPath string, prog *transferProgress, cancelled *bool) error {
	info, err := os.Stat(originalPath)
	if err != nil {
		return err
	}
	isDir := info.IsDir()

	var zipPath string
	if isDir {
		fyne.Do(func() {
			prog.label.SetText("Creating archive…")
		})
		
		zipPath, err = createZip(originalPath)
		if err != nil {
			return err
		}
		defer os.Remove(zipPath)
		originalPath = zipPath
		info, err = os.Stat(zipPath)
		if err != nil {
			return err
		}
	}

	f, err := os.Open(originalPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fyne.Do(func() {
		prog.label.SetText("Calculating checksum…")
	})

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	checksum := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	fyne.Do(func() {
		prog.label.SetText("Connecting…")
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	filename := filepath.Base(originalPath)
	if isDir {
		base := filepath.Base(strings.TrimSuffix(originalPath, filepath.Ext(originalPath)))
		filename = base + ".zip"
	}

	// Send protocol header
	if err := binary.Write(conn, binary.BigEndian, uint8(protocolVersion)); err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(len(filename))); err != nil {
		return err
	}
	if _, err := conn.Write([]byte(filename)); err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, uint64(info.Size())); err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(len(checksum))); err != nil {
		return err
	}
	if _, err := conn.Write([]byte(checksum)); err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(len(myHostname))); err != nil {
		return err
	}
	if _, err := conn.Write([]byte(myHostname)); err != nil {
		return err
	}

	// Wait for acceptance
	resp := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Time{})
	
	if resp[0] != 1 {
		return fmt.Errorf("transfer rejected by recipient")
	}

	fyne.Do(func() {
		prog.label.SetText("Transferring…")
	})

	// Send file data
	buf := make([]byte, transferBufLen)
	r := bufio.NewReader(f)
	var sent int64
	start := time.Now()
	last := start
	
	for !*cancelled {
		n, err := r.Read(buf)
		if n > 0 {
			written := 0
			for written < n && !*cancelled {
				m, werr := conn.Write(buf[written:n])
				if werr != nil {
					return werr
				}
				written += m
			}
			sent += int64(n)

			now := time.Now()
			if now.Sub(last) > updateInterval {
				spd := float64(sent) / now.Sub(start).Seconds()
				fyne.Do(func() {
					prog.bar.SetValue(float64(sent) / float64(prog.total))
					prog.label.SetText(fmt.Sprintf("%s / %s • %s/s",
						humanBytes(uint64(sent)),
						humanBytes(uint64(prog.total)),
						humanBytes(uint64(spd))))
				})
				last = now
			}
		}
		
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	
	if *cancelled {
		return fmt.Errorf("cancelled by user")
	}
	
	return nil
}

/* ------------------------------------------------------------------ */
/*  Cleanup                                                            */
/* ------------------------------------------------------------------ */
func peerCleanupLoop(ctx context.Context, ui *mainUI) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			peersMu.Lock()
			changed := false
			for k, p := range peers {
				if now.Sub(p.LastSeen) > peerTimeout {
					delete(peers, k)
					changed = true
				}
			}
			peersMu.Unlock()
			
			if changed {
				fyne.Do(func() {
					refreshDeviceList(ui)
					updateStatus(ui)
				})
			}
		case <-ctx.Done():
			return
		}
	}
}

/* ------------------------------------------------------------------ */
/*  Utils                                                              */
/* ------------------------------------------------------------------ */
func humanBytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(u), 0
	for n/div >= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}