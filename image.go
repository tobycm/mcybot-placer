package main

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

func loadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("error decoding image: %w", err)
	}

	return img, nil
}
