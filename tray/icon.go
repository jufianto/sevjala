package tray

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

const iconSize = 32

var colors = map[string]color.NRGBA{
	Green:  {0x2e, 0xa0, 0x43, 0xff},
	Yellow: {0xe3, 0xa0, 0x08, 0xff},
	Red:    {0xd0, 0x30, 0x30, 0xff},
	Grey:   {0x8a, 0x8a, 0x8a, 0xff},
}

//go:embed brand.png
var brandPNG []byte

// brandImage is the Sevjala icon at iconSize.
var brandImage = sync.OnceValue(func() *image.NRGBA {
	src, err := png.Decode(bytes.NewReader(brandPNG))
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	if err == nil {
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	}
	return img
})

// The status dot sits in the bottom-right corner, ringed in white so it
// stands out on the blue.
const (
	dotX, dotY = iconSize - 8, iconSize - 8
	dotR       = 6.5
	ringR      = 8.0
)

// badge is the Sevjala icon with the status colour as a dot.
func badge(name string) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	copy(img.Pix, brandImage().Pix)
	c := colors[name]
	for y := range iconSize {
		for x := range iconSize {
			dx, dy := float64(x-dotX), float64(y-dotY)
			switch d2 := dx*dx + dy*dy; {
			case d2 <= dotR*dotR:
				img.SetNRGBA(x, y, c)
			case d2 <= ringR*ringR:
				img.SetNRGBA(x, y, color.NRGBA{0xff, 0xff, 0xff, 0xff})
			}
		}
	}
	return img
}

// PNG is the icon for macOS and Linux.
func PNG(name string) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, badge(name))
	return b.Bytes()
}

// ICO is the icon for Windows: one 32-bit bitmap entry, which every Windows
// since XP loads (PNG inside ICO needs Vista's loader and is avoided).
func ICO(name string) []byte {
	img := badge(name)
	const header, entry, info = 6, 16, 40
	pixels := iconSize * iconSize * 4
	mask := iconSize * 4 // 1 bit per pixel, rows padded to 4 bytes
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	le([3]uint16{0, 1, 1})
	le(struct {
		W, H, Colors, Reserved uint8
		Planes, Bits           uint16
		Size, Offset           uint32
	}{iconSize, iconSize, 0, 0, 1, 32, uint32(info + pixels + mask), header + entry})
	le(struct {
		Size                 uint32
		W, H                 int32
		Planes, Bits         uint16
		Compression, ImgSize uint32
		XPPM, YPPM           int32
		Used, Important      uint32
	}{info, iconSize, 2 * iconSize, 1, 32, 0, uint32(pixels + mask), 0, 0, 0, 0})
	for y := iconSize - 1; y >= 0; y-- {
		for x := range iconSize {
			p := img.NRGBAAt(x, y)
			b.Write([]byte{p.B, p.G, p.R, p.A})
		}
	}
	b.Write(make([]byte, mask))
	return b.Bytes()
}
