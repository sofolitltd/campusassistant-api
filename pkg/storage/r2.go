package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"

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
