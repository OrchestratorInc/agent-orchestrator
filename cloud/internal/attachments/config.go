package attachments

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

func FromEnvironment(ctx context.Context, environment string) (Storage, error) {
	mode := os.Getenv("AO_CLOUD_ATTACHMENT_STORAGE")
	if mode == "" && environment == "development" {
		mode = "filesystem"
	}
	switch mode {
	case "":
		return nil, nil
	case "s3":
		return NewS3(ctx, os.Getenv("AO_CLOUD_ATTACHMENT_S3_BUCKET"), os.Getenv("AO_CLOUD_ATTACHMENT_S3_REGION"))
	case "filesystem":
		if environment != "development" && environment != "test" && environment != "local" {
			return nil, errors.New("filesystem attachments are development-only")
		}
		dir := os.Getenv("AO_DATA_DIR")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			dir = filepath.Join(home, ".ao")
		}
		dir = filepath.Join(dir, "cloud", "attachments")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "signing-key")
		key, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			_, err = file.Write(key)
			closeErr := file.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		} else if err != nil {
			return nil, err
		}
		return NewFilesystem(environment, dir, key)
	default:
		return nil, errors.New("unknown attachment storage adapter")
	}
}
