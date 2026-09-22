package artifacts

import (
	"encoding/base64"
	"hash"
	"hash/crc64"
)

const crc64NVMEReversedPolynomial uint64 = 0x9a6c9329ac4bc9b5

var crc64NVMETable = crc64.MakeTable(crc64NVMEReversedPolynomial)

func NewCRC64NVME() hash.Hash64 {
	return crc64.New(crc64NVMETable)
}

func CRC64NVMEBase64(data []byte) string {
	h := NewCRC64NVME()
	_, _ = h.Write(data)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
