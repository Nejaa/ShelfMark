package metadata

import (
	"bytes"
	"context"
	"fmt"
	"image"

	// Keep decoding in Go: the private OCR engine needs only its PNM decoder.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ocrImage converts compressed input into an 8-bit grayscale PNM image. This
// removes the native engine's dependency on PNG, JPEG, TIFF and WebP libraries.
// Check dimensions before allocating pixels: a small compressed image can still
// expand into an unreasonable allocation. Transparent areas become white paper.
func ocrImage(ctx context.Context, data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode OCR image dimensions: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 40_000_000/config.Height {
		return nil, fmt.Errorf("OCR image dimensions exceed the 40 megapixel limit")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode OCR image: %w", err)
	}
	bounds := decoded.Bounds()
	header := []byte(fmt.Sprintf("P5\n%d %d\n255\n", bounds.Dx(), bounds.Dy()))
	output := make([]byte, len(header)+bounds.Dx()*bounds.Dy())
	copy(output, header)
	offset := len(header)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// RGBA channels are premultiplied. Adding the missing alpha composites
			// against white, then standard luminance weights preserve dark lettering.
			r, g, b, a := decoded.At(x, y).RGBA()
			gray := (299*r+587*g+114*b)/1000 + (0xffff - a)
			output[offset] = byte(gray >> 8)
			offset++
		}
	}
	return output, nil
}
