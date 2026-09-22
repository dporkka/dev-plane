package artifacts

import "testing"

func TestCRC64NVMEKnownVector(t *testing.T) {
	const want = "rosUhgp5mIg="
	if got := CRC64NVMEBase64([]byte("123456789")); got != want {
		t.Fatalf("CRC64NVME = %q, want %q", got, want)
	}
}

func TestParseMultipartChecksum(t *testing.T) {
	checksum, err := ParseMultipartChecksum(MultipartChecksumCRC64NVME, "rosUhgp5mIg=")
	if err != nil {
		t.Fatal(err)
	}
	if checksum == nil || checksum.Algorithm != MultipartChecksumCRC64NVME {
		t.Fatalf("unexpected checksum: %#v", checksum)
	}
	if _, err := ParseMultipartChecksum(MultipartChecksumCRC64NVME, "bad"); err == nil {
		t.Fatal("expected invalid base64 checksum to fail")
	}
	if _, err := ParseMultipartChecksum("SHA256", "rosUhgp5mIg="); err == nil {
		t.Fatal("expected unsupported checksum algorithm to fail")
	}
}
