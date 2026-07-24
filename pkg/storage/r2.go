package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"time"

	"campusassistant-api/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Storage struct {
	client     *s3.Client
	bucketName string
	publicURL  string
}

func NewR2Storage(cfg *config.Config) (*R2Storage, error) {
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.R2AccountID),
		}, nil
	})

	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithEndpointResolverWithOptions(r2Resolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.R2AccessKeyID, cfg.R2SecretAccessKey, "")),
		awsconfig.WithRegion("auto"),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg)

	return &R2Storage{
		client:     client,
		bucketName: cfg.R2BucketName,
		publicURL:  cfg.R2PublicURL,
	}, nil
}

func (s *R2Storage) UploadFile(ctx context.Context, file *multipart.FileHeader, path string) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(path),
		Body:        f,
		ContentType: aws.String(file.Header.Get("Content-Type")),
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", s.publicURL, path), nil
}

func (s *R2Storage) UploadReader(ctx context.Context, reader io.Reader, path string, contentType string) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(path),
		Body:        reader,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", s.publicURL, path), nil
}

// PutObjectAt stores a file at an exact key with no public URL returned —
// used for private/sensitive uploads, where the caller must never hand a
// permanent public link back to the client.
func (s *R2Storage) PutObjectAt(ctx context.Context, file *multipart.FileHeader, path string) error {
	f, err := file.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(path),
		Body:        f,
		ContentType: aws.String(file.Header.Get("Content-Type")),
	})
	return err
}

// GetPresignedURL returns a time-limited URL for reading a private object —
// the only way a private (e.g. NID/Student-ID proof) object should ever be
// served to a client.
func (s *R2Storage) GetPresignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(path),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (s *R2Storage) DeleteFile(ctx context.Context, fileURL string) error {
	// Extract path from public URL
	// fileURL is e.g. "https://pub-uuid.r2.dev/banners/2026/02/uuid_name.png"
	// We need "banners/2026/02/uuid_name.png"
	path := ""
	if len(fileURL) > len(s.publicURL) {
		path = fileURL[len(s.publicURL)+1:] // +1 for the slash
	} else {
		return fmt.Errorf("invalid file URL: %s", fileURL)
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(path),
	})
	return err
}
