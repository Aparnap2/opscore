package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

func ComputeSHA256Streaming(r io.Reader) (string, error) {
	hash := sha256.New()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func GenerateIdempotencyKey(hash, tenantID string) string {
	return fmt.Sprintf("sha256:%s:tenant:%s", hash, tenantID)
}