// tools/generate_icon.go
// Icon generator for Warpify - File Sharing Design

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	size := 512
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Create modern gradient background (blue to cyan)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Diagonal gradient with radial blend
			dx := float64(x - size/2)
			dy := float64(y - size/2)
			dist := math.Sqrt(dx*dx + dy*dy)
			ratio := dist / float64(size/2)

			// Modern blue gradient
			r := uint8(40 + (60 * (1 - ratio)))
			g := uint8(120 + (80 * ratio))
			b := uint8(200 + (55 * (1 - ratio)))
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}

	// Draw circular platform with shadow
	drawCircularPlatform(img, size)

	// Draw two devices exchanging files
	drawDevice(img, size/4, size/2, true)    // Left device
	drawDevice(img, size*3/4, size/2, false) // Right device

	// Draw file being transferred (document icon in motion)
	drawTransferringFile(img, size)

	// Draw bidirectional arrows showing transfer
	drawTransferArrows(img, size)

	// Add connection waves/signals
	drawConnectionWaves(img, size)

	// Save the icon
	f, err := os.Create("Icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		panic(err)
	}

	println("✅ Icon.png created successfully!")
}

func drawCircularPlatform(img *image.RGBA, size int) {
	centerX, centerY := size/2, size/2
	radius := float64(size) / 2.3

	// Draw shadow first
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x - centerX)
			dy := float64(y - centerY + 10) // Offset for shadow
			distance := math.Sqrt(dx*dx + dy*dy)

			if distance <= radius+5 && distance >= radius {
				alpha := uint8(30 * (1 - (distance-radius)/5))
				blendColor(img, x, y, color.RGBA{0, 0, 0, alpha})
			}
		}
	}

	// Draw main platform
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x - centerX)
			dy := float64(y - centerY)
			distance := math.Sqrt(dx*dx + dy*dy)

			if distance <= radius {
				// Gradient platform
				alpha := 255
				if distance > radius-15 {
					alpha = int(255 * (radius - distance) / 15)
				}
				overlay := color.RGBA{50, 140, 220, uint8(alpha)}
				blendColor(img, x, y, overlay)
			}
		}
	}
}

func drawDevice(img *image.RGBA, centerX, centerY int, isLeft bool) {
	// Device body (laptop/computer shape)
	deviceColor := color.RGBA{255, 255, 255, 255}
	shadowColor := color.RGBA{0, 0, 0, 60}
	screenColor := color.RGBA{100, 180, 255, 255}

	deviceWidth := 50
	deviceHeight := 40

	// Draw shadow
	for y := centerY - deviceHeight/2 + 3; y < centerY+deviceHeight/2+3; y++ {
		for x := centerX - deviceWidth/2 + 2; x < centerX+deviceWidth/2+2; x++ {
			blendColor(img, x, y, shadowColor)
		}
	}

	// Draw device body
	drawRoundedRect(img, centerX-deviceWidth/2, centerY-deviceHeight/2,
		deviceWidth, deviceHeight, 5, deviceColor)

	// Draw screen
	drawRoundedRect(img, centerX-deviceWidth/2+5, centerY-deviceHeight/2+5,
		deviceWidth-10, deviceHeight-15, 3, screenColor)

	// Draw keyboard base
	for x := centerX - deviceWidth/2; x < centerX+deviceWidth/2; x++ {
		for y := centerY + deviceHeight/2 - 8; y < centerY+deviceHeight/2; y++ {
			blendColor(img, x, y, color.RGBA{220, 220, 220, 255})
		}
	}

	// Draw status indicator (active)
	indicatorColor := color.RGBA{100, 255, 100, 255}
	drawCircle(img, centerX, centerY-deviceHeight/2+10, 4, indicatorColor)
}

func drawTransferringFile(img *image.RGBA, size int) {
	// File icon in the center, slightly elevated
	fileX := size / 2
	fileY := size/2 - 30
	fileWidth := 35
	fileHeight := 45

	// File shadow
	for y := fileY; y < fileY+fileHeight; y++ {
		for x := fileX - fileWidth/2 + 2; x < fileX+fileWidth/2+2; x++ {
			blendColor(img, x, y, color.RGBA{0, 0, 0, 40})
		}
	}

	// File body (white document)
	fileColor := color.RGBA{255, 255, 255, 255}
	drawRoundedRect(img, fileX-fileWidth/2, fileY, fileWidth, fileHeight, 3, fileColor)

	// File corner fold
	foldSize := 10
	for y := fileY; y < fileY+foldSize; y++ {
		for x := fileX + fileWidth/2 - foldSize; x < fileX+fileWidth/2; x++ {
			if x-fileX-fileWidth/2+foldSize >= foldSize-(y-fileY) {
				blendColor(img, x, y, color.RGBA{200, 200, 200, 255})
			}
		}
	}

	// File lines (representing content)
	lineColor := color.RGBA{100, 150, 255, 255}
	for i := 0; i < 3; i++ {
		lineY := fileY + 15 + i*8
		for x := fileX - fileWidth/2 + 8; x < fileX+fileWidth/2-8; x++ {
			for ly := lineY; ly < lineY+2; ly++ {
				img.Set(x, ly, lineColor)
			}
		}
	}

	// Add speed lines to show motion
	for i := 0; i < 5; i++ {
		lineY := fileY + 10 + i*8
		lineX := fileX - fileWidth/2 - 10 - i*3
		lineLen := 8 + i*2
		alpha := uint8(150 - i*25)
		for x := lineX; x < lineX+lineLen; x++ {
			blendColor(img, x, lineY, color.RGBA{255, 255, 255, alpha})
		}
	}
}

func drawTransferArrows(img *image.RGBA, size int) {
	centerY := size / 2

	// Right-pointing arrow (top)
	drawArrow(img, size/4+60, centerY-15, size*3/4-60, centerY-15,
		color.RGBA{100, 255, 150, 255}, true)

	// Left-pointing arrow (bottom)
	drawArrow(img, size*3/4-60, centerY+15, size/4+60, centerY+15,
		color.RGBA{100, 200, 255, 255}, false)
}

func drawArrow(img *image.RGBA, x1, y1, x2, y2 int, col color.RGBA, pointRight bool) {
	// Draw arrow shaft with glow
	thickness := 4
	for t := -thickness; t <= thickness; t++ {
		alpha := uint8(float64(col.A) * (1 - math.Abs(float64(t))/float64(thickness+1)))
		glowCol := color.RGBA{col.R, col.G, col.B, alpha}
		drawLine(img, x1, y1+t, x2, y2+t, glowCol)
	}

	// Draw arrowhead
	headSize := 12
	var tipX, tipY int
	var base1X, base1Y, base2X, base2Y int

	if pointRight {
		tipX, tipY = x2, y2
		base1X, base1Y = x2-headSize, y2-headSize/2
		base2X, base2Y = x2-headSize, y2+headSize/2
	} else {
		tipX, tipY = x2, y2
		base1X, base1Y = x2+headSize, y2-headSize/2
		base2X, base2Y = x2+headSize, y2+headSize/2
	}

	drawFilledTriangle(img, tipX, tipY, base1X, base1Y, base2X, base2Y, col)
}

func drawConnectionWaves(img *image.RGBA, size int) {
	// centerX := size / 2
	centerY := size / 2

	// Draw signal waves emanating from devices
	waveColor := color.RGBA{255, 255, 255, 60}

	// Left device waves
	for i := 1; i <= 3; i++ {
		radius := 30 + i*12
		drawArc(img, size/4, centerY, radius, 45, 135, waveColor)
	}

	// Right device waves
	for i := 1; i <= 3; i++ {
		radius := 30 + i*12
		drawArc(img, size*3/4, centerY, radius, 225, 315, waveColor)
	}
}

func drawArc(img *image.RGBA, cx, cy, radius int, startAngle, endAngle float64, col color.RGBA) {
	for angle := startAngle; angle <= endAngle; angle += 0.5 {
		rad := angle * math.Pi / 180
		x := cx + int(float64(radius)*math.Cos(rad))
		y := cy + int(float64(radius)*math.Sin(rad))

		// Draw thicker arc
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				if dx*dx+dy*dy <= 1 {
					blendColor(img, x+dx, y+dy, col)
				}
			}
		}
	}
}

func drawRoundedRect(img *image.RGBA, x, y, width, height, radius int, col color.RGBA) {
	// Draw main rectangle
	for py := y + radius; py < y+height-radius; py++ {
		for px := x; px < x+width; px++ {
			img.Set(px, py, col)
		}
	}

	for py := y; py < y+height; py++ {
		for px := x + radius; px < x+width-radius; px++ {
			img.Set(px, py, col)
		}
	}

	// Draw corners
	drawCircleQuarter(img, x+radius, y+radius, radius, col, 3)              // Top-left
	drawCircleQuarter(img, x+width-radius, y+radius, radius, col, 2)        // Top-right
	drawCircleQuarter(img, x+radius, y+height-radius, radius, col, 0)       // Bottom-left
	drawCircleQuarter(img, x+width-radius, y+height-radius, radius, col, 1) // Bottom-right
}

func drawCircleQuarter(img *image.RGBA, cx, cy, radius int, col color.RGBA, quarter int) {
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				shouldDraw := false
				switch quarter {
				case 0:
					shouldDraw = dx <= 0 && dy >= 0 // Bottom-left
				case 1:
					shouldDraw = dx >= 0 && dy >= 0 // Bottom-right
				case 2:
					shouldDraw = dx >= 0 && dy <= 0 // Top-right
				case 3:
					shouldDraw = dx <= 0 && dy <= 0 // Top-left
				}
				if shouldDraw {
					img.Set(cx+dx, cy+dy, col)
				}
			}
		}
	}
}

func drawCircle(img *image.RGBA, cx, cy, radius int, col color.RGBA) {
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				blendColor(img, cx+dx, cy+dy, col)
			}
		}
	}
}

func drawFilledTriangle(img *image.RGBA, x1, y1, x2, y2, x3, y3 int, col color.RGBA) {
	// Find bounding box
	minX := min(x1, min(x2, x3))
	maxX := max(x1, max(x2, x3))
	minY := min(y1, min(y2, y3))
	maxY := max(y1, max(y2, y3))

	// Fill triangle using barycentric coordinates
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if isInsideTriangle(x, y, x1, y1, x2, y2, x3, y3) {
				blendColor(img, x, y, col)
			}
		}
	}
}

func isInsideTriangle(px, py, x1, y1, x2, y2, x3, y3 int) bool {
	// Calculate barycentric coordinates
	d := (y2-y3)*(x1-x3) + (x3-x2)*(y1-y3)
	if d == 0 {
		return false
	}

	a := ((y2-y3)*(px-x3) + (x3-x2)*(py-y3)) / d
	b := ((y3-y1)*(px-x3) + (x1-x3)*(py-y3)) / d
	c := 1 - a - b

	return a >= 0 && b >= 0 && c >= 0
}

func drawLine(img *image.RGBA, x1, y1, x2, y2 int, col color.RGBA) {
	// Bresenham's line algorithm
	dx := abs(x2 - x1)
	dy := abs(y2 - y1)
	sx := -1
	if x1 < x2 {
		sx = 1
	}
	sy := -1
	if y1 < y2 {
		sy = 1
	}
	err := dx - dy

	for {
		if x1 >= 0 && x1 < img.Bounds().Dx() && y1 >= 0 && y1 < img.Bounds().Dy() {
			blendColor(img, x1, y1, col)
		}

		if x1 == x2 && y1 == y2 {
			break
		}

		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x1 += sx
		}
		if e2 < dx {
			err += dx
			y1 += sy
		}
	}
}

func blendColor(img *image.RGBA, x, y int, col color.RGBA) {
	if x < 0 || x >= img.Bounds().Dx() || y < 0 || y >= img.Bounds().Dy() {
		return
	}

	existing := img.At(x, y).(color.RGBA)
	alpha := float64(col.A) / 255.0

	r := uint8(float64(col.R)*alpha + float64(existing.R)*(1-alpha))
	g := uint8(float64(col.G)*alpha + float64(existing.G)*(1-alpha))
	b := uint8(float64(col.B)*alpha + float64(existing.B)*(1-alpha))

	img.Set(x, y, color.RGBA{r, g, b, 255})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
