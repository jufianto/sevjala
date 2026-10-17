package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

// The tray shows the Sevjala icon, with the status as a dot in its corner.
func TestIconIsTheBrandWithAStatusDot(t *testing.T) {
	brand := brandImage()
	for name, want := range colors {
		img := badge(name)
		if got := img.NRGBAAt(dotX, dotY); got != want {
			t.Errorf("%s: dot is %v, want %v", name, got, want)
		}
		if got := img.NRGBAAt(16, 4); got != brand.NRGBAAt(16, 4) || got.A == 0 {
			t.Errorf("%s: the brand is not drawn: %v", name, got)
		}
	}
	if badge(Green).NRGBAAt(dotX, dotY) == badge(Red).NRGBAAt(dotX, dotY) {
		t.Error("green and red look the same")
	}
}

func TestIconFormats(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(Yellow)))
	if err != nil || img.Bounds().Dx() != iconSize {
		t.Errorf("png: %v %v", img, err)
	}
	ico := ICO(Yellow)
	var h struct {
		Reserved, Type, Count uint16
		W, H, Colors, _       uint8
		Planes, Bits          uint16
		Size, Offset          uint32
	}
	if err := binary.Read(bytes.NewReader(ico), binary.LittleEndian, &h); err != nil || h.Type != 1 || h.Count != 1 ||
		h.W != iconSize || h.Bits != 32 || int(h.Offset+h.Size) != len(ico) {
		t.Errorf("ico header %+v, %d bytes, %v", h, len(ico), err)
	}
}
