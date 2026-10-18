package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"regexp"
	"strconv"
)

// maxSide is the longer side of the picture the model reads. A larger one costs more and is
// shrunk by the providers anyway, which would move every coordinate the model reads off it.
const maxSide = 1280

// jpegQuality keeps text on the screen legible at a fraction of a PNG's size.
const jpegQuality = 80

// Picture is the screen as the model reads it.
type Picture struct {
	JPEG          []byte
	Width, Height int // of the picture
	ScreenW       int // of the screen, in the units the pointer moves in
	ScreenH       int
}

// shot takes a picture of the screen and keeps its scale for the steps that follow.
func (d *Desktop) shot(ctx context.Context) (Result, error) {
	file, dir, err := tempShot()
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	if _, err := d.steps(ctx, "taking a picture of the screen", d.be.shot(file)); err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return Result{}, fmt.Errorf("the picture of the screen was not written: %w", err)
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("the picture of the screen is not a PNG: %w", err)
	}
	screenW, screenH := src.Bounds().Dx(), src.Bounds().Dy()
	if size := d.be.size(); size != nil {
		out, err := d.steps(ctx, "reading the screen's size", size)
		if err != nil {
			return Result{}, err
		}
		if screenW, screenH, err = parseSize(out); err != nil {
			return Result{}, err
		}
	}
	pic := encode(shrink(src, maxSide))
	pic.ScreenW, pic.ScreenH = screenW, screenH
	d.mu.Lock()
	d.scale = float64(screenW) / float64(pic.Width)
	d.mu.Unlock()
	return Result{Picture: &pic, Text: fmt.Sprintf("screen %dx%d, shown as a %dx%d picture: "+
		"coordinates are this picture's pixels", screenW, screenH, pic.Width, pic.Height)}, nil
}

// numbers finds the integers of a line.
var numbers = regexp.MustCompile(`-?\d+`)

// parseSize reads "W H" or "x, y, W, H": the last two numbers.
func parseSize(out string) (int, int, error) {
	n := numbers.FindAllString(out, -1)
	if len(n) < 2 {
		return 0, 0, fmt.Errorf("the screen's size could not be read from %q", out)
	}
	w, _ := strconv.Atoi(n[len(n)-2])
	h, _ := strconv.Atoi(n[len(n)-1])
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("the screen reports no size")
	}
	return w, h, nil
}

// shrink is src no larger than side on its longer side, each pixel the average of the ones it
// covers so text stays readable. A picture already small enough is copied as it is.
func shrink(src image.Image, side int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if w > side || h > side {
		if w >= h {
			dw, dh = side, max(1, h*side/w)
		} else {
			dw, dh = max(1, w*side/h), side
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		y0, y1 := b.Min.Y+y*h/dh, b.Min.Y+max((y+1)*h/dh, y*h/dh+1)
		for x := range dw {
			x0, x1 := b.Min.X+x*w/dw, b.Min.X+max((x+1)*w/dw, x*w/dw+1)
			dst.Set(x, y, average(src, x0, y0, x1, y1))
		}
	}
	return dst
}

// average is the mean colour of src in [x0,x1)x[y0,y1).
func average(src image.Image, x0, y0, x1, y1 int) colorRGBA {
	var r, g, bl, n uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			cr, cg, cb, _ := src.At(x, y).RGBA()
			r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
		}
	}
	return colorRGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8)}
}

// colorRGBA is an opaque colour.
type colorRGBA struct{ r, g, b uint8 }

// RGBA is the colour as image/color reads it.
func (c colorRGBA) RGBA() (uint32, uint32, uint32, uint32) {
	return uint32(c.r) * 0x101, uint32(c.g) * 0x101, uint32(c.b) * 0x101, 0xffff
}

// encode is img as the JPEG the model reads.
func encode(img *image.RGBA) Picture {
	var buf bytes.Buffer
	// Writing to memory cannot fail.
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	return Picture{JPEG: buf.Bytes(), Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
}
