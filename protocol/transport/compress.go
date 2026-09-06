// Package transport contains helpers shared by report transports.
package transport

import (
	"bytes"
	"compress/gzip"
)

// GzipBytes compresses data into a complete gzip stream.
func GzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
