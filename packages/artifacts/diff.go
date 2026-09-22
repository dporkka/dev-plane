package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type DiffOperation string

const (
	DiffAdded    DiffOperation = "added"
	DiffRemoved  DiffOperation = "removed"
	DiffModified DiffOperation = "modified"
)

type SequenceChange struct {
	Operation   DiffOperation `json:"operation"`
	BeforeIndex *int          `json:"before_index,omitempty"`
	AfterIndex  *int          `json:"after_index,omitempty"`
	Before      string        `json:"before,omitempty"`
	After       string        `json:"after,omitempty"`
}

type ValueChange struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type ArtifactDiff struct {
	Kind            Kind                   `json:"kind"`
	MediaType       string                 `json:"media_type,omitempty"`
	Changed         bool                   `json:"changed"`
	Semantic        bool                   `json:"semantic"`
	Changes         []SequenceChange       `json:"changes,omitempty"`
	MetadataChanges map[string]ValueChange `json:"metadata_changes,omitempty"`
	BeforeDigest    Digest                 `json:"before_digest"`
	AfterDigest     Digest                 `json:"after_digest"`
	Warnings        []string               `json:"warnings,omitempty"`
}

func (m *Manager) DiffArtifacts(ctx context.Context, before, after Artifact) (ArtifactDiff, error) {
	if err := before.Validate(); err != nil {
		return ArtifactDiff{}, fmt.Errorf("validate before artifact: %w", err)
	}
	if err := after.Validate(); err != nil {
		return ArtifactDiff{}, fmt.Errorf("validate after artifact: %w", err)
	}
	result := ArtifactDiff{
		Kind:         after.Kind,
		MediaType:    after.Descriptor.MediaType,
		Changed:      before.Descriptor.Digest != after.Descriptor.Digest,
		BeforeDigest: before.Descriptor.Digest,
		AfterDigest:  after.Descriptor.Digest,
	}
	if !result.Changed {
		return result, nil
	}

	switch {
	case hasDerivative(before, "semantic-document") && hasDerivative(after, "semantic-document"):
		var left, right DocumentSemantic
		if err := m.decodeDerivativeJSON(ctx, before, "semantic-document", &left); err != nil {
			return ArtifactDiff{}, err
		}
		if err := m.decodeDerivativeJSON(ctx, after, "semantic-document", &right); err != nil {
			return ArtifactDiff{}, err
		}
		beforeParagraphs := make([]string, 0, len(left.Paragraphs))
		afterParagraphs := make([]string, 0, len(right.Paragraphs))
		for _, paragraph := range left.Paragraphs {
			beforeParagraphs = append(beforeParagraphs, paragraph.Text)
		}
		for _, paragraph := range right.Paragraphs {
			afterParagraphs = append(afterParagraphs, paragraph.Text)
		}
		result.Semantic = true
		result.Changes = diffSequence(beforeParagraphs, afterParagraphs)
		return result, nil

	case hasDerivative(before, "semantic-text") && hasDerivative(after, "semantic-text"):
		left, err := m.readDerivative(ctx, before, "semantic-text")
		if err != nil {
			return ArtifactDiff{}, err
		}
		right, err := m.readDerivative(ctx, after, "semantic-text")
		if err != nil {
			return ArtifactDiff{}, err
		}
		result.Semantic = true
		result.Changes = diffSequence(splitSemanticLines(string(left)), splitSemanticLines(string(right)))
		return result, nil

	case hasDerivative(before, "semantic-spreadsheet") && hasDerivative(after, "semantic-spreadsheet"):
		var left, right SpreadsheetSemantic
		if err := m.decodeDerivativeJSON(ctx, before, "semantic-spreadsheet", &left); err != nil {
			return ArtifactDiff{}, err
		}
		if err := m.decodeDerivativeJSON(ctx, after, "semantic-spreadsheet", &right); err != nil {
			return ArtifactDiff{}, err
		}
		result.Semantic = true
		result.Changes = diffSequence(flattenSpreadsheet(left), flattenSpreadsheet(right))
		return result, nil

	case hasDerivative(before, "semantic-presentation") && hasDerivative(after, "semantic-presentation"):
		var left, right PresentationSemantic
		if err := m.decodeDerivativeJSON(ctx, before, "semantic-presentation", &left); err != nil {
			return ArtifactDiff{}, err
		}
		if err := m.decodeDerivativeJSON(ctx, after, "semantic-presentation", &right); err != nil {
			return ArtifactDiff{}, err
		}
		result.Semantic = true
		result.Changes = diffSequence(flattenPresentation(left), flattenPresentation(right))
		return result, nil

	case hasDerivative(before, "semantic-image") && hasDerivative(after, "semantic-image"):
		var left, right ImageSemantic
		if err := m.decodeDerivativeJSON(ctx, before, "semantic-image", &left); err != nil {
			return ArtifactDiff{}, err
		}
		if err := m.decodeDerivativeJSON(ctx, after, "semantic-image", &right); err != nil {
			return ArtifactDiff{}, err
		}
		result.Semantic = true
		result.MetadataChanges = map[string]ValueChange{}
		addValueChange(result.MetadataChanges, "width", fmt.Sprintf("%d", left.Width), fmt.Sprintf("%d", right.Width))
		addValueChange(result.MetadataChanges, "height", fmt.Sprintf("%d", left.Height), fmt.Sprintf("%d", right.Height))
		addValueChange(result.MetadataChanges, "format", left.Format, right.Format)
		leftPreview := derivativeDigest(before, "preview-thumbnail")
		rightPreview := derivativeDigest(after, "preview-thumbnail")
		if leftPreview != rightPreview {
			result.MetadataChanges["preview_digest"] = ValueChange{Before: leftPreview, After: rightPreview}
		}
		if len(result.MetadataChanges) == 0 {
			result.Warnings = append(result.Warnings, "image bytes changed without a metadata-level semantic change")
		}
		return result, nil

	default:
		result.Warnings = append(result.Warnings, "no shared semantic representation; diff falls back to payload digest")
		return result, nil
	}
}

func (m *Manager) decodeDerivativeJSON(ctx context.Context, artifact Artifact, role string, target any) error {
	payload, err := m.readDerivative(ctx, artifact, role)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode %s derivative: %w", role, err)
	}
	return nil
}

func (m *Manager) readDerivative(ctx context.Context, artifact Artifact, role string) ([]byte, error) {
	for _, derivative := range artifact.Derivatives {
		if derivative.Role != role {
			continue
		}
		reader, err := m.store.Open(ctx, derivative.Descriptor.Digest)
		if err != nil {
			return nil, fmt.Errorf("open %s derivative: %w", role, err)
		}
		defer reader.Close()
		payload, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read %s derivative: %w", role, err)
		}
		return payload, nil
	}
	return nil, fmt.Errorf("artifact derivative %q not found", role)
}

func hasDerivative(artifact Artifact, role string) bool {
	for _, derivative := range artifact.Derivatives {
		if derivative.Role == role {
			return true
		}
	}
	return false
}

func derivativeDigest(artifact Artifact, role string) string {
	for _, derivative := range artifact.Derivatives {
		if derivative.Role == role {
			return derivative.Descriptor.Digest.String()
		}
	}
	return ""
}

func addValueChange(changes map[string]ValueChange, key, before, after string) {
	if before != after {
		changes[key] = ValueChange{Before: before, After: after}
	}
}

func splitSemanticLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

// diffSequence computes an LCS-based semantic diff for normal documents and
// falls back to prefix/suffix comparison for unusually large inputs to keep
// memory bounded.
func diffSequence(before, after []string) []SequenceChange {
	const maxCells = 1_000_000
	if len(before) == 0 && len(after) == 0 {
		return nil
	}
	if len(before)*len(after) > maxCells {
		return diffSequenceBounded(before, after)
	}

	width := len(after) + 1
	table := make([]uint32, (len(before)+1)*width)
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			index := i*width + j
			if before[i] == after[j] {
				table[index] = table[(i+1)*width+j+1] + 1
			} else {
				down := table[(i+1)*width+j]
				right := table[i*width+j+1]
				if down >= right {
					table[index] = down
				} else {
					table[index] = right
				}
			}
		}
	}

	var changes []SequenceChange
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		if before[i] == after[j] {
			i++
			j++
			continue
		}
		down := table[(i+1)*width+j]
		right := table[i*width+j+1]
		if down == right {
			switch {
			case i+1 < len(before) && before[i+1] == after[j]:
				beforeIndex := i
				changes = append(changes, SequenceChange{
					Operation: DiffRemoved, BeforeIndex: &beforeIndex, Before: before[i],
				})
				i++
				continue
			case j+1 < len(after) && before[i] == after[j+1]:
				afterIndex := j
				changes = append(changes, SequenceChange{
					Operation: DiffAdded, AfterIndex: &afterIndex, After: after[j],
				})
				j++
				continue
			default:
				beforeIndex, afterIndex := i, j
				changes = append(changes, SequenceChange{
					Operation: DiffModified, BeforeIndex: &beforeIndex, AfterIndex: &afterIndex,
					Before: before[i], After: after[j],
				})
				i++
				j++
				continue
			}
		}
		if down >= right {
			beforeIndex := i
			changes = append(changes, SequenceChange{
				Operation: DiffRemoved, BeforeIndex: &beforeIndex, Before: before[i],
			})
			i++
		} else {
			afterIndex := j
			changes = append(changes, SequenceChange{
				Operation: DiffAdded, AfterIndex: &afterIndex, After: after[j],
			})
			j++
		}
	}
	for ; i < len(before); i++ {
		beforeIndex := i
		changes = append(changes, SequenceChange{
			Operation: DiffRemoved, BeforeIndex: &beforeIndex, Before: before[i],
		})
	}
	for ; j < len(after); j++ {
		afterIndex := j
		changes = append(changes, SequenceChange{
			Operation: DiffAdded, AfterIndex: &afterIndex, After: after[j],
		})
	}
	return changes
}

func diffSequenceBounded(before, after []string) []SequenceChange {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	var changes []SequenceChange
	beforeEnd := len(before) - suffix
	afterEnd := len(after) - suffix
	paired := beforeEnd - prefix
	if other := afterEnd - prefix; other < paired {
		paired = other
	}
	for offset := 0; offset < paired; offset++ {
		i, j := prefix+offset, prefix+offset
		if before[i] != after[j] {
			beforeIndex, afterIndex := i, j
			changes = append(changes, SequenceChange{
				Operation: DiffModified, BeforeIndex: &beforeIndex, AfterIndex: &afterIndex,
				Before: before[i], After: after[j],
			})
		}
	}
	for i := prefix + paired; i < beforeEnd; i++ {
		beforeIndex := i
		changes = append(changes, SequenceChange{Operation: DiffRemoved, BeforeIndex: &beforeIndex, Before: before[i]})
	}
	for j := prefix + paired; j < afterEnd; j++ {
		afterIndex := j
		changes = append(changes, SequenceChange{Operation: DiffAdded, AfterIndex: &afterIndex, After: after[j]})
	}
	return changes
}


func flattenSpreadsheet(value SpreadsheetSemantic) []string {
	var result []string
	for _, sheet := range value.Sheets {
		for _, cell := range sheet.Cells {
			location := sheet.Name + "!" + cell.Ref
			switch {
			case cell.Formula != "":
				result = append(result, location+" = "+cell.Value+" [formula: "+cell.Formula+"]")
			case cell.Value != "":
				result = append(result, location+" = "+cell.Value)
			default:
				result = append(result, location+" =")
			}
		}
	}
	return result
}

func flattenPresentation(value PresentationSemantic) []string {
	var result []string
	for _, slide := range value.Slides {
		for index, paragraph := range slide.Paragraphs {
			result = append(result, fmt.Sprintf("slide:%d/paragraph:%d %s", slide.Index, index+1, paragraph))
		}
	}
	return result
}
