package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDetectDOCXByOOXMLContents(t *testing.T) {
	payload := makeDOCX(t, []string{"First paragraph", "Second paragraph"})
	reader := bytes.NewReader(payload)
	format, err := DetectFormat("renamed.bin", reader, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if format.Kind != KindDocument || format.MediaType != MediaTypeDOCX {
		t.Fatalf("format = %+v, want DOCX", format)
	}
}

func TestManagerAnalyzeDOCXStoresSemanticRepresentation(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	payload := makeDOCX(t, []string{"Quarterly proposal", "Annual cost: $21,000"})
	artifact, err := manager.PutArtifact(context.Background(), PutArtifactRequest{
		Path:      "docs/proposal.docx",
		Kind:      KindDocument,
		MediaType: MediaTypeDOCX,
		Reader:    bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, analysis, err := manager.AnalyzeArtifact(
		context.Background(),
		NewAdapterRegistry(NewDOCXAdapter()),
		artifact,
		AnalyzeOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Adapter != "docx" {
		t.Fatalf("adapter = %q, want docx", analysis.Adapter)
	}
	if updated.SemanticDigest == nil {
		t.Fatal("semantic digest was not attached")
	}
	semanticDerivative := findDerivative(updated.Derivatives, "semantic-document")
	if semanticDerivative == nil {
		t.Fatal("semantic-document derivative missing")
	}
	reader, err := store.Open(context.Background(), semanticDerivative.Descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	semanticPayload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var semantic DocumentSemantic
	if err := json.Unmarshal(semanticPayload, &semantic); err != nil {
		t.Fatal(err)
	}
	if len(semantic.Paragraphs) != 2 ||
		semantic.Paragraphs[0].Text != "Quarterly proposal" ||
		semantic.Paragraphs[1].Text != "Annual cost: $21,000" {
		t.Fatalf("unexpected semantics: %+v", semantic)
	}
}

func TestManagerAnalyzeImageStoresThumbnail(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}

	source := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, colorForPoint(x, y))
		}
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, source); err != nil {
		t.Fatal(err)
	}
	artifact, err := manager.PutArtifact(context.Background(), PutArtifactRequest{
		Path:      "assets/hero.bin",
		Kind:      KindBinary,
		MediaType: "application/octet-stream",
		Reader:    bytes.NewReader(payload.Bytes()),
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, analysis, err := manager.AnalyzeArtifact(
		context.Background(),
		NewAdapterRegistry(NewImageAdapter(512)),
		artifact,
		AnalyzeOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != KindImage || updated.Descriptor.MediaType != "image/png" {
		t.Fatalf("detected artifact = kind %q media %q", updated.Kind, updated.Descriptor.MediaType)
	}
	if analysis.Metadata["image.width"] != "2000" || analysis.Metadata["image.height"] != "1000" {
		t.Fatalf("image metadata = %+v", analysis.Metadata)
	}

	preview := findDerivative(updated.Derivatives, "preview-thumbnail")
	if preview == nil {
		t.Fatal("preview-thumbnail derivative missing")
	}
	reader, err := store.Open(context.Background(), preview.Descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || config.Width != 512 || config.Height != 256 {
		t.Fatalf("preview = %s %dx%d, want png 512x256", format, config.Width, config.Height)
	}
}

func TestManagerAnalyzePDFStoresTextAndPreview(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	preview := onePixelPNG(t)
	runner := fakePDFRunner{
		text:    []byte("Page one text\n"),
		preview: preview,
	}
	artifact, err := manager.PutArtifact(context.Background(), PutArtifactRequest{
		Path:      "docs/report.pdf",
		Kind:      KindPDF,
		MediaType: "application/pdf",
		Reader:    strings.NewReader("%PDF-1.7\n%%EOF"),
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, analysis, err := manager.AnalyzeArtifact(
		context.Background(),
		NewAdapterRegistry(NewPDFAdapter(runner)),
		artifact,
		AnalyzeOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Metadata["pdf.semantic_status"] != "extracted" ||
		analysis.Metadata["pdf.preview_status"] != "rendered" {
		t.Fatalf("PDF analysis metadata = %+v", analysis.Metadata)
	}
	if updated.SemanticDigest == nil {
		t.Fatal("PDF semantic digest missing")
	}
	if findDerivative(updated.Derivatives, "semantic-text") == nil {
		t.Fatal("PDF semantic-text derivative missing")
	}
	if findDerivative(updated.Derivatives, "preview-page-1") == nil {
		t.Fatal("PDF preview derivative missing")
	}
}

func TestPDFAdapterDegradesWhenToolsUnavailable(t *testing.T) {
	file := tempArtifactFile(t, []byte("%PDF-1.4\n%%EOF"))
	adapter := NewPDFAdapter(unavailablePDFRunner{})
	analysis, err := adapter.Analyze(context.Background(), AnalysisInput{
		Path: "doc.pdf",
		Format: DetectedFormat{Kind: KindPDF, MediaType: "application/pdf"},
		File: file,
		Size: 15,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Metadata["pdf.semantic_status"] != "tool_unavailable" ||
		analysis.Metadata["pdf.preview_status"] != "tool_unavailable" {
		t.Fatalf("unexpected graceful degradation: %+v", analysis)
	}
}

func TestDefaultAdapterRegistry(t *testing.T) {
	names := NewDefaultAdapterRegistry().Names()
	got := strings.Join(names, ",")
	if got != "docx,image,pdf" {
		t.Fatalf("registry names = %q", got)
	}
}

type fakePDFRunner struct {
	text    []byte
	preview []byte
}

func (f fakePDFRunner) ExtractText(context.Context, string, int64) ([]byte, error) {
	return append([]byte(nil), f.text...), nil
}
func (f fakePDFRunner) RenderFirstPage(context.Context, string, int64) ([]byte, error) {
	return append([]byte(nil), f.preview...), nil
}

type unavailablePDFRunner struct{}

func (unavailablePDFRunner) ExtractText(context.Context, string, int64) ([]byte, error) {
	return nil, ErrPDFToolsUnavailable
}
func (unavailablePDFRunner) RenderFirstPage(context.Context, string, int64) ([]byte, error) {
	return nil, ErrPDFToolsUnavailable
}

func makeDOCX(t *testing.T, paragraphs []string) []byte {
	t.Helper()
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	contentTypes, err := archive.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = contentTypes.Write([]byte(`<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	document, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	var xml bytes.Buffer
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, paragraph := range paragraphs {
		xml.WriteString("<w:p><w:r><w:t>")
		xml.WriteString(xmlEscape(paragraph))
		xml.WriteString("</w:t></w:r></w:p>")
	}
	xml.WriteString("</w:body></w:document>")
	_, _ = document.Write(xml.Bytes())
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func findDerivative(derivatives []DerivativeRef, role string) *DerivativeRef {
	for i := range derivatives {
		if derivatives[i].Role == role {
			return &derivatives[i]
		}
	}
	return nil
}

func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, colorForPoint(1, 1))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func colorForPoint(x, y int) color.RGBA {
	return color.RGBA{R: byte(x), G: byte(y), B: byte(x + y), A: 255}
}

func tempArtifactFile(t *testing.T, payload []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "artifact-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}
