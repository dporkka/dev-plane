package artifacts

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const SemanticPresentationMediaType = "application/vnd.dev-plane.presentation-semantics.v1+json"

type PPTXAdapter struct{}

func NewPPTXAdapter() PPTXAdapter   { return PPTXAdapter{} }
func (PPTXAdapter) Name() string    { return "pptx" }
func (PPTXAdapter) Version() string { return "1" }
func (PPTXAdapter) Supports(format DetectedFormat) bool {
	return format.MediaType == MediaTypePPTX
}

type PresentationSemantic struct {
	Slides []PresentationSlide `json:"slides"`
}

type PresentationSlide struct {
	Index      int      `json:"index"`
	Paragraphs []string `json:"paragraphs,omitempty"`
}

func (PPTXAdapter) Analyze(_ context.Context, input AnalysisInput) (AdapterAnalysis, error) {
	archive, err := zip.NewReader(input.File, input.Size)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("open PPTX archive: %w", err)
	}
	files := zipFileMap(archive.File)
	order, err := parsePresentationOrder(files)
	if err != nil {
		return AdapterAnalysis{}, err
	}
	if len(order) == 0 {
		return AdapterAnalysis{}, fmt.Errorf("PPTX presentation contains no slides")
	}

	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	var (
		semantic  PresentationSemantic
		usedBytes int64
		truncated bool
	)
	for index, slidePath := range order {
		slideFile := files[slidePath]
		if slideFile == nil {
			continue
		}
		paragraphs, consumed, wasTruncated, err := parseSlideParagraphs(slideFile, maxBytes-usedBytes)
		if err != nil {
			return AdapterAnalysis{}, fmt.Errorf("parse slide %d: %w", index+1, err)
		}
		usedBytes += consumed
		semantic.Slides = append(semantic.Slides, PresentationSlide{
			Index:      index + 1,
			Paragraphs: paragraphs,
		})
		if wasTruncated || usedBytes >= maxBytes {
			truncated = true
			break
		}
	}
	payload, err := json.Marshal(semantic)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("marshal PPTX semantics: %w", err)
	}
	metadata := map[string]string{
		"presentation.slide_count": fmt.Sprintf("%d", len(semantic.Slides)),
	}
	var warnings []string
	if truncated {
		metadata["presentation.semantic_truncated"] = "true"
		warnings = append(warnings, "presentation semantic representation reached analysis byte limit")
	}
	return AdapterAnalysis{
		Adapter:  "pptx",
		Version:  "1",
		Metadata: metadata,
		Semantic: &GeneratedPayload{
			Role:      "semantic-presentation",
			MediaType: SemanticPresentationMediaType,
			Data:      payload,
		},
		Warnings: warnings,
	}, nil
}

func parsePresentationOrder(files map[string]*zip.File) ([]string, error) {
	presentation := files["ppt/presentation.xml"]
	if presentation == nil {
		return nil, fmt.Errorf("PPTX ppt/presentation.xml is missing")
	}
	relationships, err := parseOOXMLRelationships(files["ppt/_rels/presentation.xml.rels"])
	if err != nil {
		return nil, err
	}
	reader, err := presentation.Open()
	if err != nil {
		return nil, fmt.Errorf("open PPTX presentation.xml: %w", err)
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var order []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return order, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode PPTX presentation.xml: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sldId" {
			continue
		}
		var rid string
		for _, attr := range start.Attr {
			if attr.Name.Local == "id" && attr.Name.Space != "" {
				rid = attr.Value
			}
		}
		if rid == "" {
			for _, attr := range start.Attr {
				if attr.Name.Local == "id" {
					rid = attr.Value
				}
			}
		}
		target := relationships[rid]
		if target != "" {
			order = append(order, normalizeOOXMLTarget("ppt", target))
		}
	}
}

func parseSlideParagraphs(file *zip.File, maxBytes int64) ([]string, int64, bool, error) {
	if maxBytes <= 0 {
		return nil, 0, true, nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil, 0, false, err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var (
		paragraphs []string
		current    strings.Builder
		inPara     bool
		inText     bool
		usedBytes  int64
		truncated  bool
	)
	flush := func() {
		value := strings.TrimSpace(current.String())
		current.Reset()
		if value == "" {
			return
		}
		if usedBytes+int64(len(value)) > maxBytes {
			truncated = true
			return
		}
		usedBytes += int64(len(value))
		paragraphs = append(paragraphs, value)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if inPara {
				flush()
			}
			return paragraphs, usedBytes, truncated, nil
		}
		if err != nil {
			return nil, usedBytes, truncated, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "p":
				if inPara {
					flush()
				}
				inPara = true
			case "t":
				if inPara {
					inText = true
				}
			}
		case xml.CharData:
			if inPara && inText && !truncated {
				current.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				inText = false
			case "p":
				if inPara {
					flush()
					inPara = false
					if truncated {
						return paragraphs, usedBytes, true, nil
					}
				}
			}
		}
	}
}
