package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

var ErrPDFToolsUnavailable = errors.New("PDF tools unavailable")

type PDFToolRunner interface {
	ExtractText(ctx context.Context, inputPath string, maxBytes int64) ([]byte, error)
	RenderFirstPage(ctx context.Context, inputPath string, maxBytes int64) ([]byte, error)
}

type PopplerRunner struct{}

func (PopplerRunner) ExtractText(ctx context.Context, inputPath string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	command := exec.CommandContext(ctx, "pdftotext", "-layout", inputPath, "-")
	var output cappedBuffer
	output.max = maxBytes
	command.Stdout = &output
	command.Stderr = &output.stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrPDFToolsUnavailable
		}
		return nil, fmt.Errorf("pdftotext: %w: %s", err, output.stderr.String())
	}
	return output.Bytes(), nil
}

func (PopplerRunner) RenderFirstPage(ctx context.Context, inputPath string, maxBytes int64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "dev-plane-pdf-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	prefix := filepath.Join(dir, "page")
	command := exec.CommandContext(ctx,
		"pdftoppm",
		"-f", "1",
		"-singlefile",
		"-png",
		"-scale-to", "1600",
		inputPath,
		prefix,
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrPDFToolsUnavailable
		}
		return nil, fmt.Errorf("pdftoppm: %w: %s", err, stderr.String())
	}
	file, err := os.Open(prefix + ".png")
	if err != nil {
		return nil, fmt.Errorf("open PDF preview: %w", err)
	}
	defer file.Close()
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	return io.ReadAll(io.LimitReader(file, maxBytes+1))
}

type PDFAdapter struct {
	runner PDFToolRunner
}

func NewPDFAdapter(runner PDFToolRunner) PDFAdapter {
	return PDFAdapter{runner: runner}
}
func (PDFAdapter) Name() string    { return "pdf" }
func (PDFAdapter) Version() string { return "1" }
func (PDFAdapter) Supports(format DetectedFormat) bool {
	return format.MediaType == "application/pdf"
}

func (a PDFAdapter) Analyze(ctx context.Context, input AnalysisInput) (AdapterAnalysis, error) {
	analysis := AdapterAnalysis{
		Adapter: "pdf", Version: "1",
		Metadata: map[string]string{},
	}
	if a.runner == nil {
		analysis.Metadata["pdf.semantic_status"] = "tool_unavailable"
		analysis.Warnings = append(analysis.Warnings, "PDF semantic extraction requires Poppler or another PDFToolRunner")
		return analysis, nil
	}

	text, err := a.runner.ExtractText(ctx, input.File.Name(), input.MaxBytes)
	switch {
	case err == nil:
		if len(text) > 0 {
			analysis.Semantic = &GeneratedPayload{
				Role: "semantic-text", MediaType: "text/plain; charset=utf-8", Data: text,
			}
			analysis.Metadata["pdf.semantic_status"] = "extracted"
		} else {
			analysis.Metadata["pdf.semantic_status"] = "empty"
		}
	case errors.Is(err, ErrPDFToolsUnavailable):
		analysis.Metadata["pdf.semantic_status"] = "tool_unavailable"
		analysis.Warnings = append(analysis.Warnings, "pdftotext is unavailable")
	default:
		analysis.Metadata["pdf.semantic_status"] = "failed"
		analysis.Warnings = append(analysis.Warnings, err.Error())
	}

	preview, err := a.runner.RenderFirstPage(ctx, input.File.Name(), input.MaxBytes)
	switch {
	case err == nil && len(preview) > 0:
		if int64(len(preview)) > input.MaxBytes && input.MaxBytes > 0 {
			analysis.Warnings = append(analysis.Warnings, "PDF preview exceeded analysis byte limit")
		} else {
			analysis.Derivatives = append(analysis.Derivatives, GeneratedPayload{
				Role: "preview-page-1", MediaType: "image/png", Data: preview,
			})
			analysis.Metadata["pdf.preview_status"] = "rendered"
		}
	case errors.Is(err, ErrPDFToolsUnavailable):
		analysis.Metadata["pdf.preview_status"] = "tool_unavailable"
		analysis.Warnings = append(analysis.Warnings, "pdftoppm is unavailable")
	case err != nil:
		analysis.Metadata["pdf.preview_status"] = "failed"
		analysis.Warnings = append(analysis.Warnings, err.Error())
	}
	return analysis, nil
}

type cappedBuffer struct {
	buf       bytes.Buffer
	stderr    bytes.Buffer
	max       int64
	truncated bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.max - int64(w.buf.Len())
	if remaining <= 0 {
		w.truncated = true
		return original, nil
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	_, _ = w.buf.Write(p)
	return original, nil
}

func (w *cappedBuffer) Bytes() []byte {
	return append([]byte(nil), w.buf.Bytes()...)
}
