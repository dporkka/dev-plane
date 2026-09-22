package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
)

type ImageAdapter struct {
	MaxPreviewDimension int
}

func NewImageAdapter(maxPreviewDimension int) ImageAdapter {
	if maxPreviewDimension <= 0 {
		maxPreviewDimension = 1024
	}
	return ImageAdapter{MaxPreviewDimension: maxPreviewDimension}
}

func (ImageAdapter) Name() string    { return "image" }
func (ImageAdapter) Version() string { return "1" }
func (ImageAdapter) Supports(format DetectedFormat) bool {
	return strings.HasPrefix(format.MediaType, "image/")
}

type ImageSemantic struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

func (a ImageAdapter) Analyze(_ context.Context, input AnalysisInput) (AdapterAnalysis, error) {
	if _, err := input.File.Seek(0, io.SeekStart); err != nil {
		return AdapterAnalysis{}, err
	}
	config, format, err := image.DecodeConfig(input.File)
	if err != nil {
		return AdapterAnalysis{
			Adapter: "image", Version: "1",
			Metadata: map[string]string{"image.decode_status": "unsupported"},
			Warnings: []string{"image format could not be decoded by the built-in adapter"},
		}, nil
	}
	semantic := ImageSemantic{Width: config.Width, Height: config.Height, Format: format}
	semanticPayload, err := json.Marshal(semantic)
	if err != nil {
		return AdapterAnalysis{}, err
	}

	analysis := AdapterAnalysis{
		Adapter: "image",
		Version: "1",
		Metadata: map[string]string{
			"image.width":  fmt.Sprintf("%d", config.Width),
			"image.height": fmt.Sprintf("%d", config.Height),
			"image.format": format,
		},
		Semantic: &GeneratedPayload{
			Role: "semantic-image", MediaType: SemanticImageMediaType, Data: semanticPayload,
		},
	}

	if _, err := input.File.Seek(0, io.SeekStart); err != nil {
		return AdapterAnalysis{}, err
	}
	source, _, err := image.Decode(input.File)
	if err != nil {
		analysis.Warnings = append(analysis.Warnings, "image preview could not be decoded")
		return analysis, nil
	}
	preview := resizeNearest(source, a.MaxPreviewDimension)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, preview); err != nil {
		return AdapterAnalysis{}, fmt.Errorf("encode image preview: %w", err)
	}
	analysis.Derivatives = append(analysis.Derivatives, GeneratedPayload{
		Role: "preview-thumbnail", MediaType: "image/png", Data: encoded.Bytes(),
	})
	return analysis, nil
}

func resizeNearest(source image.Image, maxDimension int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxDimension && height <= maxDimension {
		return source
	}
	scale := float64(maxDimension) / float64(width)
	if height > width {
		scale = float64(maxDimension) / float64(height)
	}
	dstWidth := int(float64(width) * scale)
	dstHeight := int(float64(height) * scale)
	if dstWidth < 1 {
		dstWidth = 1
	}
	if dstHeight < 1 {
		dstHeight = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for y := 0; y < dstHeight; y++ {
		sourceY := bounds.Min.Y + y*height/dstHeight
		for x := 0; x < dstWidth; x++ {
			sourceX := bounds.Min.X + x*width/dstWidth
			target.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return target
}
