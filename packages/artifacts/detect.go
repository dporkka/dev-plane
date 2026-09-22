package artifacts

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	MediaTypeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MediaTypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	MediaTypePPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

func DetectFormat(path string, reader io.ReaderAt, size int64) (DetectedFormat, error) {
	extension := strings.ToLower(filepath.Ext(path))
	format := formatFromExtension(extension)

	headSize := int64(512)
	if size < headSize {
		headSize = size
	}
	head := make([]byte, headSize)
	if headSize > 0 {
		n, err := reader.ReadAt(head, 0)
		if err != nil && err != io.EOF {
			return DetectedFormat{}, fmt.Errorf("read artifact signature: %w", err)
		}
		head = head[:n]
	}

	if len(head) >= 5 && string(head[:5]) == "%PDF-" {
		return DetectedFormat{Kind: KindPDF, MediaType: "application/pdf", Extension: extension}, nil
	}

	if len(head) >= 4 && string(head[:2]) == "PK" {
		if office := detectOOXML(reader, size, extension); office.MediaType != "" {
			return office, nil
		}
	}

	if len(head) > 0 {
		sniffed := http.DetectContentType(head)
		if strings.HasPrefix(sniffed, "image/") {
			return DetectedFormat{Kind: KindImage, MediaType: sniffed, Extension: extension}, nil
		}
		if strings.HasPrefix(sniffed, "text/") && utf8.Valid(head) {
			if format.MediaType != "" && strings.HasPrefix(format.MediaType, "text/") {
				return format, nil
			}
			return DetectedFormat{Kind: KindText, MediaType: sniffed, Extension: extension}, nil
		}
	}

	if format.MediaType != "" {
		return format, nil
	}
	mediaType := mime.TypeByExtension(extension)
	if mediaType != "" {
		mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
		return DetectedFormat{Kind: kindFromMediaType(mediaType), MediaType: mediaType, Extension: extension}, nil
	}
	return DetectedFormat{Kind: KindBinary, MediaType: "application/octet-stream", Extension: extension}, nil
}

func detectOOXML(reader io.ReaderAt, size int64, extension string) DetectedFormat {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return DetectedFormat{}
	}
	var (
		hasWord  bool
		hasExcel bool
		hasPPT   bool
	)
	for _, file := range archive.File {
		switch {
		case strings.HasPrefix(file.Name, "word/"):
			hasWord = true
		case strings.HasPrefix(file.Name, "xl/"):
			hasExcel = true
		case strings.HasPrefix(file.Name, "ppt/"):
			hasPPT = true
		}
	}
	switch {
	case hasWord:
		return DetectedFormat{Kind: KindDocument, MediaType: MediaTypeDOCX, Extension: extension}
	case hasExcel:
		return DetectedFormat{Kind: KindSpreadsheet, MediaType: MediaTypeXLSX, Extension: extension}
	case hasPPT:
		return DetectedFormat{Kind: KindPresentation, MediaType: MediaTypePPTX, Extension: extension}
	default:
		return DetectedFormat{}
	}
}

func formatFromExtension(extension string) DetectedFormat {
	switch extension {
	case ".docx":
		return DetectedFormat{Kind: KindDocument, MediaType: MediaTypeDOCX, Extension: extension}
	case ".xlsx":
		return DetectedFormat{Kind: KindSpreadsheet, MediaType: MediaTypeXLSX, Extension: extension}
	case ".pptx":
		return DetectedFormat{Kind: KindPresentation, MediaType: MediaTypePPTX, Extension: extension}
	case ".pdf":
		return DetectedFormat{Kind: KindPDF, MediaType: "application/pdf", Extension: extension}
	case ".md", ".markdown":
		return DetectedFormat{Kind: KindText, MediaType: "text/markdown", Extension: extension}
	case ".txt":
		return DetectedFormat{Kind: KindText, MediaType: "text/plain", Extension: extension}
	case ".csv", ".tsv", ".parquet", ".jsonl":
		return DetectedFormat{Kind: KindDataset, MediaType: mediaTypeForDataset(extension), Extension: extension}
	case ".png":
		return DetectedFormat{Kind: KindImage, MediaType: "image/png", Extension: extension}
	case ".jpg", ".jpeg":
		return DetectedFormat{Kind: KindImage, MediaType: "image/jpeg", Extension: extension}
	case ".gif":
		return DetectedFormat{Kind: KindImage, MediaType: "image/gif", Extension: extension}
	case ".webp":
		return DetectedFormat{Kind: KindImage, MediaType: "image/webp", Extension: extension}
	default:
		return DetectedFormat{Extension: extension}
	}
}

func mediaTypeForDataset(extension string) string {
	switch extension {
	case ".csv":
		return "text/csv"
	case ".tsv":
		return "text/tab-separated-values"
	case ".parquet":
		return "application/vnd.apache.parquet"
	case ".jsonl":
		return "application/x-ndjson"
	default:
		return "application/octet-stream"
	}
}

func kindFromMediaType(mediaType string) Kind {
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return KindImage
	case strings.HasPrefix(mediaType, "audio/"):
		return KindAudio
	case strings.HasPrefix(mediaType, "video/"):
		return KindVideo
	case strings.HasPrefix(mediaType, "text/"):
		return KindText
	case mediaType == "application/pdf":
		return KindPDF
	default:
		return KindBinary
	}
}
