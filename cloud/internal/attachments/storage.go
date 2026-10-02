package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const MaxCount = 8
const MaxBytes = 10 << 20
const MaxTotalBytes = 25 << 20
const UploadTTL = 10 * time.Minute
const ReadTTL = 5 * time.Minute

var ErrInvalid = errors.New("invalid image attachment")
var Extensions = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif", "image/bmp": ".bmp"}

type Metadata struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
	Status   string `json:"status"`
}

type UploadGrant struct {
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	ExpiresAt time.Time         `json:"expiresAt"`
}
type ReadGrant struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Storage interface {
	Upload(context.Context, string, Metadata) (UploadGrant, error)
	Open(context.Context, string) (io.ReadCloser, error)
	PutVerified(context.Context, string, Metadata, []byte) error
	Read(context.Context, string) (ReadGrant, error)
	Delete(context.Context, string) error
}

func ValidateMetadata(m Metadata) error {
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != m.SHA256 || m.Size < 1 || m.Size > MaxBytes || Extensions[m.MIMEType] == "" || len(m.Filename) == 0 || len(m.Filename) > 255 {
		return ErrInvalid
	}
	return nil
}

// Verify reads a bounded object once. Promotion uses these exact bytes, never a
// later copy of the upload key that a still-live grant could overwrite.
func Verify(r io.Reader, m Metadata) ([]byte, error) {
	if err := ValidateMetadata(m); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, m.Size+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != m.Size || hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, ErrInvalid
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "bmp": "image/bmp"}[format]
	if err != nil || mime != m.MIMEType || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 25_000_000 {
		return nil, ErrInvalid
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}
