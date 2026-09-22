package artifacts

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestS3SigV4MatchesAWSHeaderExample(t *testing.T) {
	store := &S3Store{
		region:          "us-east-1",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?lifecycle", nil)
	if err != nil {
		t.Fatal(err)
	}
	store.sign(req, emptySHA256Hex, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))

	got := req.Header.Get("Authorization")
	wantSignature := "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"
	if !strings.Contains(got, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("unexpected signed headers: %s", got)
	}
	if !strings.HasSuffix(got, "Signature="+wantSignature) {
		t.Fatalf("signature mismatch:\n got: %s\nwant suffix: Signature=%s", got, wantSignature)
	}
}
