package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type DOCXAdapter struct{}

func NewDOCXAdapter() DOCXAdapter { return DOCXAdapter{} }
func (DOCXAdapter) Name() string  { return "docx" }
func (DOCXAdapter) Version() string { return "1" }

func (DOCXAdapter) Supports(format DetectedFormat) bool {
	return format.MediaType == MediaTypeDOCX
}

type DocumentSemantic struct {
	Paragraphs []DocumentParagraph `json:"paragraphs"`
}

type DocumentParagraph struct {
	Text string `json:"text"`
}

func (DOCXAdapter) Analyze(_ context.Context, input AnalysisInput) (AdapterAnalysis, error) {
	archive, err := zip.NewReader(input.File, input.Size)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("open DOCX archive: %w", err)
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return AdapterAnalysis{}, fmt.Errorf("DOCX word/document.xml is missing")
	}

	reader, err := document.Open()
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("open DOCX document.xml: %w", err)
	}
	defer reader.Close()

	semantic, truncated, err := extractDOCXParagraphs(reader, input.MaxBytes)
	if err != nil {
		return AdapterAnalysis{}, err
	}
	payload, err := json.Marshal(semantic)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("marshal DOCX semantics: %w", err)
	}

	metadata := map[string]string{
		"document.paragraph_count": fmt.Sprintf("%d", len(semantic.Paragraphs)),
	}
	var warnings []string
	if truncated {
		metadata["document.semantic_truncated"] = "true"
		warnings = append(warnings, "semantic representation reached analysis byte limit")
	}
	return AdapterAnalysis{
		Adapter:  "docx",
		Version:  "1",
		Metadata: metadata,
		Semantic: &GeneratedPayload{
			Role:      "semantic-document",
			MediaType: SemanticDocumentMediaType,
			Data:      payload,
		},
		Warnings: warnings,
	}, nil
}

func extractDOCXParagraphs(reader io.Reader, maxBytes int64) (DocumentSemantic, bool, error) {
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	decoder := xml.NewDecoder(reader)
	var (
		semantic  DocumentSemantic
		current   bytes.Buffer
		inPara    bool
		truncated bool
		written   int64
	)
	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			semantic.Paragraphs = append(semantic.Paragraphs, DocumentParagraph{Text: text})
		}
		current.Reset()
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return DocumentSemantic{}, false, fmt.Errorf("decode DOCX XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "p":
				if inPara {
					flush()
				}
				inPara = true
			case "tab":
				if inPara && written < maxBytes {
					current.WriteByte('\t')
					written++
				}
			case "br", "cr":
				if inPara && written < maxBytes {
					current.WriteByte('\n')
					written++
				}
			}
		case xml.CharData:
			if !inPara || written >= maxBytes {
				if inPara && len(value) > 0 {
					truncated = true
				}
				continue
			}
			remaining := maxBytes - written
			data := []byte(value)
			if int64(len(data)) > remaining {
				data = data[:remaining]
				truncated = true
			}
			current.Write(data)
			written += int64(len(data))
		case xml.EndElement:
			if value.Name.Local == "p" && inPara {
				flush()
				inPara = false
			}
		}
	}
	if inPara {
		flush()
	}
	return semantic, truncated, nil
}
