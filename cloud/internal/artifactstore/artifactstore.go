// Package artifactstore keeps cloud session artifact files in a versioned S3
// bucket. Every object lives under orgs/{orgId}/sessions/{sessionId}/{path},
// and every read names the exact object version Postgres committed.
package artifactstore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// maxKeyBytes is the S3 object key length limit.
const maxKeyBytes = 1024

var (
	// ErrNotFound reports that the object version does not exist.
	ErrNotFound = errors.New("artifact object not found")
	// ErrRangeNotSatisfiable reports that a requested byte range starts past
	// the end of the object.
	ErrRangeNotSatisfiable = errors.New("artifact range not satisfiable")
	// ErrChecksumMismatch reports that S3 rejected an upload whose body did not
	// match its claimed SHA-256.
	ErrChecksumMismatch = errors.New("artifact checksum mismatch")
)

// Kind values, matching the local session artifact kinds.
const (
	KindHTML     = "html"
	KindMarkdown = "markdown"
	KindFile     = "file"
)

// Config locates the artifact bucket. Endpoint is set only for an
// S3-compatible server such as local MinIO; it switches to path-style
// addressing.
type Config struct {
	Bucket   string
	Region   string
	Endpoint string
}

// Store reads and writes artifact objects in one bucket.
type Store struct {
	client *s3.Client
	bucket string
}

// Head is the stored size and SHA-256 of one object version.
type Head struct {
	Size   int64
	SHA256 []byte
}

// Object is one object version's body. The caller closes Body. Size is the
// length of Body; ContentRange is set when Body is only a byte range.
type Object struct {
	Body         io.ReadCloser
	Size         int64
	ContentRange string
}

// New builds a Store with credentials from the default AWS chain.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.Bucket) == "" || strings.TrimSpace(cfg.Region) == "" {
		return nil, errors.New("artifact bucket and region are required")
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
			options.UsePathStyle = true
		}
	})
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Key builds the object key for one artifact path of a session.
func Key(orgID, sessionID, cleanPath string) (string, error) {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(sessionID) == "" {
		return "", errors.New("artifact key requires an organization and session")
	}
	if cleanPath == "" {
		return "", domain.ErrInvalidArtifactPath
	}
	key := "orgs/" + orgID + "/sessions/" + sessionID + "/" + cleanPath
	if len(key) > maxKeyBytes {
		return "", fmt.Errorf("%w: path is too long", domain.ErrInvalidArtifactPath)
	}
	return key, nil
}

// KindOf returns the artifact kind for a path.
func KindOf(cleanPath string) string {
	switch strings.ToLower(path.Ext(cleanPath)) {
	case ".html", ".htm":
		return KindHTML
	case ".md", ".markdown":
		return KindMarkdown
	}
	return KindFile
}

// ContentType returns the Content-Type served for a path, from a fixed list so
// it does not depend on the host's MIME tables.
func ContentType(cleanPath string) string {
	ext := strings.ToLower(path.Ext(cleanPath))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log", ".csv", ".tsv", ".yaml", ".yml", ".toml":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".apng":
		return "image/apng"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".mp4":
		return "video/mp4"
	case ".m4v":
		return "video/x-m4v"
	case ".mov":
		return "video/quicktime"
	case ".ogv":
		return "video/ogg"
	case ".webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

// Put streams size bytes from body to key with S3 verifying sha256Sum, and
// returns the new object version. A body that does not match sha256Sum is
// rejected by S3 without creating a version and returns ErrChecksumMismatch.
func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64, sha256Sum []byte) (string, error) {
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(s.bucket),
		Key:            aws.String(key),
		Body:           readerOnly{body},
		ContentLength:  aws.Int64(size),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(sha256Sum)),
	}, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	if err != nil {
		if checksumRejected(err) {
			return "", ErrChecksumMismatch
		}
		return "", fmt.Errorf("put artifact object: %w", err)
	}
	versionID := aws.ToString(out.VersionId)
	if versionID == "" {
		return "", errors.New("put artifact object: bucket returned no version id (is versioning enabled?)")
	}
	return versionID, nil
}

// Head returns the size and stored SHA-256 of one object version.
func (s *Store) Head(ctx context.Context, key, versionID string) (Head, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		VersionId:    aws.String(versionID),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		if notFound(err) {
			return Head{}, ErrNotFound
		}
		return Head{}, fmt.Errorf("head artifact object: %w", err)
	}
	head := Head{Size: aws.ToInt64(out.ContentLength)}
	if encoded := aws.ToString(out.ChecksumSHA256); encoded != "" {
		// A multipart checksum carries a "-N" part count and is not the
		// object's SHA-256; leave SHA256 empty for it.
		if sum, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			head.SHA256 = sum
		}
	}
	return head, nil
}

// Get opens one object version.
func (s *Store) Get(ctx context.Context, key, versionID string) (Object, error) {
	return s.GetRange(ctx, key, versionID, "")
}

// GetRange opens one object version, or the byte range byteRange (an HTTP
// Range header value such as "bytes=0-99") of it when that is not empty. A
// range that starts past the end of the object returns ErrRangeNotSatisfiable.
func (s *Store) GetRange(ctx context.Context, key, versionID, byteRange string) (Object, error) {
	input := &s3.GetObjectInput{
		Bucket:    aws.String(s.bucket),
		Key:       aws.String(key),
		VersionId: aws.String(versionID),
	}
	if byteRange != "" {
		input.Range = aws.String(byteRange)
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		if notFound(err) {
			return Object{}, ErrNotFound
		}
		var responseErr *smithyhttp.ResponseError
		if byteRange != "" && errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusRequestedRangeNotSatisfiable {
			return Object{}, ErrRangeNotSatisfiable
		}
		return Object{}, fmt.Errorf("get artifact object: %w", err)
	}
	return Object{Body: out.Body, Size: aws.ToInt64(out.ContentLength), ContentRange: aws.ToString(out.ContentRange)}, nil
}

// DeleteVersion permanently removes one object version.
func (s *Store) DeleteVersion(ctx context.Context, key, versionID string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:    aws.String(s.bucket),
		Key:       aws.String(key),
		VersionId: aws.String(versionID),
	})
	if err != nil && !notFound(err) {
		return fmt.Errorf("delete artifact object version: %w", err)
	}
	return nil
}

// readerOnly hides any Seek method so the SDK streams the body once.
type readerOnly struct{ io.Reader }

func checksumRejected(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "BadDigest", "XAmzContentChecksumMismatch", "XAmzContentSHA256Mismatch", "InvalidDigest":
		return true
	}
	return false
}

func notFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NoSuchVersion", "NotFound":
			return true
		}
	}
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound
}
