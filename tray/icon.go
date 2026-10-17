package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

const iconSize = 32

var colors = map[string]color.NRGBA{
	Green:  {0x2e, 0xa0, 0x43, 0xff},
	Yellow: {0xe3, 0xa0, 0x08, 0xff},
	Red:    {0xd0, 0x30, 0x30, 0xff},
	Grey:   {0x8a, 0x8a, 0x8a, 0xff},
}

// circle is a filled disc with a soft edge on a transparent square.
func circle(name string) *image.NRGBA {
	c := colors[name]
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	const r = iconSize/2 - 2
	for y := range iconSize {
		for x := range iconSize {
			dx, dy := float64(x)+0.5-iconSize/2, float64(y)+0.5-iconSize/2
			d2 := dx*dx + dy*dy
			switch {
			case d2 <= (r-1)*(r-1):
				img.SetNRGBA(x, y, c)
			case d2 <= r*r:
				edge := c
				edge.A = 0x80
				img.SetNRGBA(x, y, edge)
			}
		}
	}
	return img
}

// PNG is the icon for macOS and Linux.
func PNG(name string) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, circle(name))
	return b.Bytes()
}

// ICO is the icon for Windows: one 32-bit bitmap entry, which every Windows
// since XP loads (PNG inside ICO needs Vista's loader and is avoided).
func ICO(name string) []byte {
	img := circle(name)
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
