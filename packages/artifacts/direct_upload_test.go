package artifacts

import "testing"

func TestMultipartPartSize(t *testing.T) {
	tests := []struct {
		name      string
		size      int64
		requested int64
		wantParts int
	}{
		{name: "small", size: 10 << 20, wantParts: 1},
		{name: "large", size: 130 << 20, wantParts: 3},
		{name: "requested", size: 20 << 20, requested: 5 << 20, wantParts: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			partSize, parts, err := MultipartPartSize(tt.size, tt.requested)
			if err != nil {
				t.Fatal(err)
			}
			if partSize < MinMultipartPartSize || partSize > MaxMultipartPartSize {
				t.Fatalf("part size %d outside limits", partSize)
			}
			if parts != tt.wantParts {
				t.Fatalf("parts = %d, want %d", parts, tt.wantParts)
			}
		})
	}
}

func TestMultipartPartSizeRaisesPartSizeForTenThousandLimit(t *testing.T) {
	size := int64(5 << 40)
	partSize, parts, err := MultipartPartSize(size, DefaultMultipartPartSize)
	if err != nil {
		t.Fatal(err)
	}
	if parts > MaxMultipartParts {
		t.Fatalf("parts = %d, max %d", parts, MaxMultipartParts)
	}
	if partSize <= DefaultMultipartPartSize {
		t.Fatalf("part size = %d, expected it to grow above default", partSize)
	}
}


func TestMultipartPartSizeRejectsZero(t *testing.T) {
	if _, _, err := MultipartPartSize(0, 0); err == nil {
		t.Fatal("expected zero-byte multipart upload to be rejected")
	}
}
