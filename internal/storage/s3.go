package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const defaultS3Region = "us-east-1"

type S3Config struct {
	Endpoint       string
	Region         string
	ForcePathStyle bool
}

type S3Backend struct {
	client *s3.Client
	bucket string
}

func NewS3Service(ctx context.Context, bucket string, maxUploadBytes int64, cfg S3Config) (*Service, error) {
	backend, err := NewS3Backend(ctx, bucket, cfg)
	if err != nil {
		return nil, err
	}
	return NewService("s3", bucket, maxUploadBytes, backend)
}

func NewS3Backend(ctx context.Context, bucket string, cfg S3Config) (*S3Backend, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("s3 storage bucket is required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("s3 storage endpoint is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultS3Region
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		o.HTTPClient = &s3DiagnosticHTTPClient{client: o.HTTPClient}
		if isGCSXMLAPIEndpoint(endpoint) {
			o.HTTPSignerV4 = newGCSV4Signer(*o)
		}
	})
	return &S3Backend{client: client, bucket: bucket}, nil
}

func isGCSXMLAPIEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && strings.EqualFold(parsed.Hostname(), "storage.googleapis.com")
}

type gcsV4Signer struct {
	signer s3.HTTPSignerV4
}

func newGCSV4Signer(options s3.Options) *gcsV4Signer {
	return &gcsV4Signer{signer: v4.NewSigner(func(o *v4.SignerOptions) {
		o.Logger = options.Logger
		o.LogSigning = options.ClientLogMode.IsSigning()
		o.DisableURIPathEscaping = true
	})}
}

func (s *gcsV4Signer) SignHTTP(
	ctx context.Context,
	credentials aws.Credentials,
	request *http.Request,
	payloadHash string,
	service string,
	region string,
	signingTime time.Time,
	optFns ...func(*v4.SignerOptions),
) error {
	if request.Method == http.MethodPut && request.Header.Get("If-None-Match") == "*" {
		// GCS does not support S3's create-only PUT precondition, and its x-goog
		// equivalent cannot be mixed with the AWS SigV4 x-amz headers.
		request.Header.Del("If-None-Match")
	}

	acceptEncoding := append([]string(nil), request.Header.Values("Accept-Encoding")...)
	if len(acceptEncoding) > 0 {
		request.Header.Del("Accept-Encoding")
		defer func() {
			request.Header["Accept-Encoding"] = acceptEncoding
		}()
	}

	return s.signer.SignHTTP(ctx, credentials, request, payloadHash, service, region, signingTime, optFns...)
}

func (b *S3Backend) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (WrittenObject, error) {
	if maxBytes <= 0 {
		return WrittenObject{}, errors.New("max bytes must be positive")
	}
	key, err := validateS3ObjectKey(key)
	if err != nil {
		return WrittenObject{}, err
	}

	// PutObject needs the length up front and a body it can rewind to sign
	// and retry. A seekable source, such as an upload spooled to a temp
	// file, is measured and then streamed from where it stands; anything
	// else is buffered in memory first.
	body, ok := r.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, r: r}, maxBytes+1))
		if err != nil {
			return WrittenObject{}, err
		}
		body = bytes.NewReader(data)
	}
	start, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return WrittenObject{}, err
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(&contextReader{ctx: ctx, r: body}, maxBytes+1))
	if err != nil {
		return WrittenObject{}, err
	}
	if size > maxBytes {
		return WrittenObject{}, ErrTooLarge
	}
	if _, err := body.Seek(start, io.SeekStart); err != nil {
		return WrittenObject{}, err
	}

	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		IfNoneMatch:   aws.String("*"),
	})
	if err != nil {
		return WrittenObject{}, mapS3Error(err)
	}
	return WrittenObject{
		Size:   size,
		SHA256: hexHash(hasher),
	}, nil
}

func (b *S3Backend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	key, err := validateS3ObjectKey(key)
	if err != nil {
		return nil, err
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, mapS3Error(err)
	}
	return out.Body, nil
}

func (b *S3Backend) Delete(ctx context.Context, key string) error {
	key, err := validateS3ObjectKey(key)
	if err != nil {
		return err
	}
	if _, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return mapS3Error(err)
	}
	if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return mapS3Error(err)
	}
	return nil
}

func validateS3ObjectKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return "", ErrInvalidKey
	}
	cleanKey := path.Clean(key)
	if cleanKey == "." || cleanKey == ".." || strings.HasPrefix(cleanKey, "../") {
		return "", ErrInvalidKey
	}
	return cleanKey, nil
}

func mapS3Error(err error) error {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound":
		return ErrNotFound
	case "PreconditionFailed", "ConditionalRequestConflict":
		return ErrExists
	default:
		return withS3Diagnostics(err)
	}
}
