package artifacts

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
)

const SemanticSpreadsheetMediaType = "application/vnd.dev-plane.spreadsheet-semantics.v1+json"

type XLSXAdapter struct{}

func NewXLSXAdapter() XLSXAdapter   { return XLSXAdapter{} }
func (XLSXAdapter) Name() string    { return "xlsx" }
func (XLSXAdapter) Version() string { return "1" }
func (XLSXAdapter) Supports(format DetectedFormat) bool {
	return format.MediaType == MediaTypeXLSX
}

type SpreadsheetSemantic struct {
	Sheets []SpreadsheetSheet `json:"sheets"`
}

type SpreadsheetSheet struct {
	Name  string            `json:"name"`
	Cells []SpreadsheetCell `json:"cells,omitempty"`
}

type SpreadsheetCell struct {
	Ref     string `json:"ref"`
	Type    string `json:"type,omitempty"`
	Value   string `json:"value,omitempty"`
	Formula string `json:"formula,omitempty"`
}

type workbookSheet struct {
	Name string
	RID  string
}

type xlsxRelationship struct {
	ID     string
	Target string
}

func (XLSXAdapter) Analyze(_ context.Context, input AnalysisInput) (AdapterAnalysis, error) {
	archive, err := zip.NewReader(input.File, input.Size)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("open XLSX archive: %w", err)
	}
	files := zipFileMap(archive.File)
	workbookFile := files["xl/workbook.xml"]
	if workbookFile == nil {
		return AdapterAnalysis{}, fmt.Errorf("XLSX xl/workbook.xml is missing")
	}
	sheets, err := parseWorkbookSheets(workbookFile)
	if err != nil {
		return AdapterAnalysis{}, err
	}
	relationships, err := parseOOXMLRelationships(files["xl/_rels/workbook.xml.rels"])
	if err != nil {
		return AdapterAnalysis{}, err
	}
	sharedStrings, err := parseSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return AdapterAnalysis{}, err
	}

	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	var (
		semantic   SpreadsheetSemantic
		totalCells int
		usedBytes  int64
		truncated  bool
	)
	for _, sheet := range sheets {
		target := relationships[sheet.RID]
		if target == "" {
			continue
		}
		target = normalizeOOXMLTarget("xl", target)
		worksheet := files[target]
		if worksheet == nil {
			continue
		}
		cells, consumed, wasTruncated, err := parseWorksheetCells(worksheet, sharedStrings, maxBytes-usedBytes)
		if err != nil {
			return AdapterAnalysis{}, fmt.Errorf("parse worksheet %q: %w", sheet.Name, err)
		}
		usedBytes += consumed
		if wasTruncated {
			truncated = true
		}
		totalCells += len(cells)
		semantic.Sheets = append(semantic.Sheets, SpreadsheetSheet{Name: sheet.Name, Cells: cells})
		if usedBytes >= maxBytes {
			truncated = true
			break
		}
	}

	payload, err := json.Marshal(semantic)
	if err != nil {
		return AdapterAnalysis{}, fmt.Errorf("marshal XLSX semantics: %w", err)
	}
	metadata := map[string]string{
		"spreadsheet.sheet_count": fmt.Sprintf("%d", len(semantic.Sheets)),
		"spreadsheet.cell_count":  fmt.Sprintf("%d", totalCells),
	}
	var warnings []string
	if truncated {
		metadata["spreadsheet.semantic_truncated"] = "true"
		warnings = append(warnings, "spreadsheet semantic representation reached analysis byte limit")
	}
	return AdapterAnalysis{
		Adapter:  "xlsx",
		Version:  "1",
		Metadata: metadata,
		Semantic: &GeneratedPayload{
			Role:      "semantic-spreadsheet",
			MediaType: SemanticSpreadsheetMediaType,
			Data:      payload,
		},
		Warnings: warnings,
	}, nil
}

func parseWorkbookSheets(file *zip.File) ([]workbookSheet, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open XLSX workbook.xml: %w", err)
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var sheets []workbookSheet
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return sheets, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode XLSX workbook.xml: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		var item workbookSheet
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "name":
				item.Name = attr.Value
			case "id":
				item.RID = attr.Value
			}
		}
		if item.Name != "" && item.RID != "" {
			sheets = append(sheets, item)
		}
	}
}

func parseOOXMLRelationships(file *zip.File) (map[string]string, error) {
	result := map[string]string{}
	if file == nil {
		return result, nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open OOXML relationships: %w", err)
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode OOXML relationships: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		var rel xlsxRelationship
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "Id":
				rel.ID = attr.Value
			case "Target":
				rel.Target = attr.Value
			}
		}
		if rel.ID != "" && rel.Target != "" {
			result[rel.ID] = rel.Target
		}
	}
}

func parseSharedStrings(file *zip.File) ([]string, error) {
	if file == nil {
		return nil, nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open XLSX sharedStrings.xml: %w", err)
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var (
		values  []string
		current strings.Builder
		inSI    bool
	)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode XLSX sharedStrings.xml: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "si" {
				current.Reset()
				inSI = true
			}
		case xml.CharData:
			if inSI {
				current.Write([]byte(value))
			}
		case xml.EndElement:
			if value.Name.Local == "si" && inSI {
				values = append(values, current.String())
				inSI = false
			}
		}
	}
}

func parseWorksheetCells(file *zip.File, sharedStrings []string, maxBytes int64) ([]SpreadsheetCell, int64, bool, error) {
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
		cells     []SpreadsheetCell
		current   *SpreadsheetCell
		capture   string
		text      strings.Builder
		usedBytes int64
		truncated bool
	)
	flushCapture := func() {
		if current == nil || capture == "" {
			text.Reset()
			capture = ""
			return
		}
		value := text.String()
		switch capture {
		case "v":
			current.Value = value
		case "f":
			current.Formula = value
		case "t":
			if current.Value == "" {
				current.Value = value
			} else {
				current.Value += value
			}
		}
		text.Reset()
		capture = ""
	}
	flushCell := func() {
		if current == nil {
			return
		}
		if current.Type == "s" && current.Value != "" {
			var index int
			if _, err := fmt.Sscanf(current.Value, "%d", &index); err == nil && index >= 0 && index < len(sharedStrings) {
				current.Value = sharedStrings[index]
			}
		}
		cost := int64(len(current.Ref) + len(current.Type) + len(current.Value) + len(current.Formula))
		if usedBytes+cost > maxBytes {
			truncated = true
			current = nil
			return
		}
		usedBytes += cost
		cells = append(cells, *current)
		current = nil
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			flushCapture()
			flushCell()
			return cells, usedBytes, truncated, nil
		}
		if err != nil {
			return nil, usedBytes, truncated, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "c":
				flushCell()
				current = &SpreadsheetCell{}
				for _, attr := range value.Attr {
					switch attr.Name.Local {
					case "r":
						current.Ref = attr.Value
					case "t":
						current.Type = attr.Value
					}
				}
			case "v", "f", "t":
				if current != nil {
					flushCapture()
					capture = value.Name.Local
				}
			}
		case xml.CharData:
			if current != nil && capture != "" {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			if current != nil && value.Name.Local == capture {
				flushCapture()
			}
			if value.Name.Local == "c" {
				flushCell()
				if truncated {
					return cells, usedBytes, true, nil
				}
			}
		}
	}
}

func zipFileMap(files []*zip.File) map[string]*zip.File {
	result := make(map[string]*zip.File, len(files))
	for _, file := range files {
		result[path.Clean(strings.TrimPrefix(file.Name, "/"))] = file
	}
	return result
}

func normalizeOOXMLTarget(base, target string) string {
	target = strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(target, "/") {
		return path.Clean(strings.TrimPrefix(target, "/"))
	}
	return path.Clean(path.Join(base, target))
}
