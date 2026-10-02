package attachments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// The default credential chain uses the deployment's IAM role. Bucket public
// access blocking, encryption, lifecycle and exact-origin CORS are infrastructure
// requirements, not permissions the running application should manage.
func NewS3(ctx context.Context, bucket, region string) (*S3, error) {
	if bucket == "" || region == "" {
		return nil, errors.New("attachment S3 bucket and region are required")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: bucket}, nil
}
func (s *S3) Upload(ctx context.Context, key string, m Metadata) (UploadGrant, error) {
	if err := ValidateMetadata(m); err != nil {
		return UploadGrant{}, err
	}
	sum, _ := hex.DecodeString(m.SHA256)
	fields := map[string]string{"Content-Type": m.MIMEType, "x-amz-checksum-algorithm": "SHA256", "x-amz-checksum-sha256": base64.StdEncoding.EncodeToString(sum), "x-amz-server-side-encryption": "AES256"}
	conditions := []any{[]any{"content-length-range", m.Size, m.Size}}
	for k, v := range fields {
		conditions = append(conditions, map[string]string{k: v})
	}
	grant, err := s.presign.PresignPostObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, func(o *s3.PresignPostOptions) { o.Expires = UploadTTL; o.Conditions = conditions })
	if err != nil {
		return UploadGrant{}, err
	}
	for k, v := range fields {
		grant.Values[k] = v
	}
	policy, err := base64.StdEncoding.DecodeString(grant.Values["policy"])
	var deadline struct {
		Expiration time.Time `json:"expiration"`
	}
	if err != nil || json.Unmarshal(policy, &deadline) != nil || deadline.Expiration.IsZero() {
		return UploadGrant{}, errors.New("invalid S3 upload policy expiration")
	}
	return UploadGrant{URL: grant.URL, Fields: grant.Values, ExpiresAt: deadline.Expiration}, nil
}
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
func (s *S3) PutVerified(ctx context.Context, key string, m Metadata, data []byte) error {
	sum, _ := hex.DecodeString(m.SHA256)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentLength: aws.Int64(m.Size), ContentType: aws.String(m.MIMEType), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(sum)), ServerSideEncryption: types.ServerSideEncryptionAes256})
	return err
}
func (s *S3) Read(ctx context.Context, key string) (ReadGrant, error) {
	out, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, func(o *s3.PresignOptions) { o.Expires = ReadTTL })
	if err != nil {
		return ReadGrant{}, err
	}
	return ReadGrant{URL: out.URL, ExpiresAt: time.Now().Add(ReadTTL)}, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}
