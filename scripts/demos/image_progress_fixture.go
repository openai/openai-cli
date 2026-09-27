package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// All three stages share a 2:1 canvas. The round sun and square chest button
// make stretching visible; the recording never squeezes pictures to fit a frame.
// These are synthetic drawings, not a claim about generated image quality.
func syntheticProgressPNG(stage int) string {
	picture := image.NewRGBA(image.Rect(0, 0, 128, 64))
	fill := func(rect image.Rectangle, c color.RGBA) {
		draw.Draw(picture, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	fill(picture.Bounds(), color.RGBA{0, 0, 95, 255})
	fill(image.Rect(0, 56, 128, 64), color.RGBA{95, 95, 135, 255})
	for y := 6; y < 22; y++ {
		for x := 100; x < 116; x++ {
			dx, dy := 2*x-215, 2*y-27
			if dx*dx+dy*dy <= 16*16 {
				picture.SetRGBA(x, y, color.RGBA{255, 215, 95, 255})
			}
		}
	}
	orange := color.RGBA{175, 135, 95, 255}
	if stage > 0 {
		orange = color.RGBA{255, 175, 0, 255}
	}
	fill(image.Rect(62, 6, 66, 12), orange)
	fill(image.Rect(44, 12, 84, 38), orange)
	fill(image.Rect(50, 40, 78, 54), orange)
	fill(image.Rect(44, 42, 48, 54), orange)
	fill(image.Rect(80, 42, 84, 54), orange)
	fill(image.Rect(50, 56, 58, 62), orange)
	fill(image.Rect(70, 56, 78, 62), orange)
	if stage > 0 {
		fill(image.Rect(48, 16, 80, 34), color.RGBA{0, 0, 0, 255})
	}
	if stage > 1 {
		light := color.RGBA{255, 255, 215, 255}
		fill(image.Rect(54, 20, 60, 26), light)
		fill(image.Rect(68, 20, 74, 26), light)
		fill(image.Rect(60, 28, 68, 30), light)
		fill(image.Rect(60, 44, 68, 52), light)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}
