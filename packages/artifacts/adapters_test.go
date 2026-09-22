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
	if got != "docx,image,pdf,pptx,xlsx" {
		t.Fatalf("registry names = %q", got)
	}
}

func TestXLSXAdapterExtractsCellsAndFormulas(t *testing.T) {
	file := tempArtifactFile(t, makeXLSX(t))
	adapter := NewXLSXAdapter()
	analysis, err := adapter.Analyze(context.Background(), AnalysisInput{
		Path: "budget.xlsx",
		Format: DetectedFormat{Kind: KindSpreadsheet, MediaType: MediaTypeXLSX},
		File: file,
		Size: fileSize(t, file),
		MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Semantic == nil || analysis.Semantic.Role != "semantic-spreadsheet" {
		t.Fatalf("unexpected XLSX analysis: %+v", analysis)
	}
	var semantic SpreadsheetSemantic
	if err := json.Unmarshal(analysis.Semantic.Data, &semantic); err != nil {
		t.Fatal(err)
	}
	if len(semantic.Sheets) != 1 || semantic.Sheets[0].Name != "Budget" {
		t.Fatalf("unexpected sheets: %+v", semantic.Sheets)
	}
	cells := semantic.Sheets[0].Cells
	if len(cells) != 3 {
		t.Fatalf("cells = %+v, want 3 cells", cells)
	}
	if cells[0].Ref != "A1" || cells[0].Value != "Revenue" {
		t.Fatalf("A1 = %+v", cells[0])
	}
	if cells[2].Ref != "A3" || cells[2].Formula != "A2*2" || cells[2].Value != "200" {
		t.Fatalf("A3 = %+v", cells[2])
	}
}

func TestPPTXAdapterPreservesPresentationOrder(t *testing.T) {
	file := tempArtifactFile(t, makePPTX(t))
	adapter := NewPPTXAdapter()
	analysis, err := adapter.Analyze(context.Background(), AnalysisInput{
		Path: "deck.pptx",
		Format: DetectedFormat{Kind: KindPresentation, MediaType: MediaTypePPTX},
		File: file,
		Size: fileSize(t, file),
		MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var semantic PresentationSemantic
	if analysis.Semantic == nil {
		t.Fatal("PPTX semantic payload missing")
	}
	if err := json.Unmarshal(analysis.Semantic.Data, &semantic); err != nil {
		t.Fatal(err)
	}
	if len(semantic.Slides) != 2 {
		t.Fatalf("slides = %+v, want 2", semantic.Slides)
	}
	if got := strings.Join(semantic.Slides[0].Paragraphs, "|"); got != "Second slide|Details" {
		t.Fatalf("slide 1 text = %q", got)
	}
	if got := strings.Join(semantic.Slides[1].Paragraphs, "|"); got != "First slide" {
		t.Fatalf("slide 2 text = %q", got)
	}
}

func TestDiffSpreadsheetSemantics(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	before := semanticPayloadArtifact(t, store, "book-before", KindSpreadsheet, MediaTypeXLSX, "semantic-spreadsheet", SpreadsheetSemantic{
		Sheets: []SpreadsheetSheet{{Name: "Budget", Cells: []SpreadsheetCell{{Ref: "A1", Value: "100"}}}},
	})
	after := semanticPayloadArtifact(t, store, "book-after", KindSpreadsheet, MediaTypeXLSX, "semantic-spreadsheet", SpreadsheetSemantic{
		Sheets: []SpreadsheetSheet{{Name: "Budget", Cells: []SpreadsheetCell{{Ref: "A1", Value: "125"}}}},
	})
	diff, err := manager.DiffArtifacts(context.Background(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Semantic || len(diff.Changes) != 1 || diff.Changes[0].Operation != DiffModified {
		t.Fatalf("spreadsheet diff = %+v", diff)
	}
	if !strings.Contains(diff.Changes[0].Before, "Budget!A1 = 100") ||
		!strings.Contains(diff.Changes[0].After, "Budget!A1 = 125") {
		t.Fatalf("unexpected spreadsheet change: %+v", diff.Changes[0])
	}
}

func semanticPayloadArtifact(t *testing.T, store BlobStore, identity string, kind Kind, mediaType, role string, value any) Artifact {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Put(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	descriptor.MediaType = "application/json"
	digest := descriptor.Digest
	return Artifact{
		Path: identity,
		Kind: kind,
		Descriptor: Descriptor{Digest: HashBytes([]byte(identity)), Size: int64(len(identity)), MediaType: mediaType},
		SemanticDigest: &digest,
		Derivatives: []DerivativeRef{{Role: role, Descriptor: descriptor}},
	}
}

func fileSize(t *testing.T, file *os.File) int64 {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func makeXLSX(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	writeZipFile(t, archive, "xl/workbook.xml", `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets>
</workbook>`)
	writeZipFile(t, archive, "xl/_rels/workbook.xml.rels", `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Target="worksheets/sheet1.xml"/>
</Relationships>`)
	writeZipFile(t, archive, "xl/sharedStrings.xml", `<?xml version="1.0"?>
<sst><si><t>Revenue</t></si></sst>`)
	writeZipFile(t, archive, "xl/worksheets/sheet1.xml", `<?xml version="1.0"?>
<worksheet><sheetData><row>
  <c r="A1" t="s"><v>0</v></c>
  <c r="A2"><v>100</v></c>
  <c r="A3"><f>A2*2</f><v>200</v></c>
</row></sheetData></worksheet>`)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func makePPTX(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	writeZipFile(t, archive, "ppt/presentation.xml", `<?xml version="1.0"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
 xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <p:sldIdLst><p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId1"/></p:sldIdLst>
</p:presentation>`)
	writeZipFile(t, archive, "ppt/_rels/presentation.xml.rels", `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Target="slides/slide1.xml"/>
 <Relationship Id="rId2" Target="slides/slide2.xml"/>
</Relationships>`)
	writeZipFile(t, archive, "ppt/slides/slide1.xml", `<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><a:p><a:r><a:t>First slide</a:t></a:r></a:p></p:cSld></p:sld>`)
	writeZipFile(t, archive, "ppt/slides/slide2.xml", `<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><a:p><a:r><a:t>Second slide</a:t></a:r></a:p><a:p><a:r><a:t>Details</a:t></a:r></a:p></p:cSld></p:sld>`)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func writeZipFile(t *testing.T, archive *zip.Writer, name, content string) {
	t.Helper()
	writer, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
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
