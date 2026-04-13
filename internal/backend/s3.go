package backend

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"filegate/config"
)

type s3Backend struct {
	name   string
	bucket string
	client *s3.Client
}

func newS3Backend(cfg config.BackendConfig) (Backend, error) {
	// 端点检查和处理
	endpoint := strings.TrimSpace(cfg.Config.Endpoint)

	if strings.TrimSpace(cfg.Config.Bucket) == "" {
		return nil, fmt.Errorf("s3 backend %q requires config.bucket", cfg.Name)
	}

	if endpoint != "" && !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	// 超时设置检查
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// 区域设置检查
	region := strings.TrimSpace(cfg.Config.Region)
	if region == "" {
		region = "us-east-1"
	}

	awsCfg := aws.Config{
		Region:     region,
		HTTPClient: &http.Client{Timeout: timeout},
	}

	accessKey := strings.TrimSpace(cfg.Config.AccessKey)
	secretKey := strings.TrimSpace(cfg.Config.SecretKey)

	// 凭证检查
	// 如果提供了凭证，则必须成对出现
	if (accessKey == "") != (secretKey == "") {
		return nil, fmt.Errorf("s3 backend %q requires both access_key and secret_key", cfg.Name)
	}
	// 只有两者都存在时才设置静态凭证
	if accessKey != "" && secretKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentialsProvider(
			accessKey, secretKey, "",
		)
	}
	// 函数选项模式，传入一个匿名函数，实际内部会调用这个函数并传入一个 *s3.Options 对象
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true // 强制使用路径风格访问 S3
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	return &s3Backend{
		name:   cfg.Name,
		bucket: strings.TrimSpace(cfg.Config.Bucket),
		client: client,
	}, nil
}

func (b *s3Backend) Name() string {
	return b.name
}

func (b *s3Backend) Fetch(ctx context.Context, objectPath string) (*Object, error) {
	// 对象路径检查
	if strings.TrimSpace(objectPath) == "" {
		return nil, fmt.Errorf("object path is empty")
	}

	key := strings.TrimLeft(objectPath, "/")
	if key == "" {
		return nil, fmt.Errorf("object path is empty")
	}
	// 发起 GetObject 请求
	resp, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("request backend %q: %w", b.name, err)
	}

	headers := make(http.Header)
	// 头部格式化，将 S3 响应的元数据转换为标准 HTTP 头部格式
	if resp.ETag != nil {
		headers.Set("Etag", strings.Trim(*resp.ETag, "\""))
	}
	if resp.LastModified != nil {
		headers.Set("Last-Modified", resp.LastModified.UTC().Format(time.RFC1123))
	}
	if resp.CacheControl != nil {
		headers.Set("Cache-Control", *resp.CacheControl)
	}
	if resp.ContentDisposition != nil {
		headers.Set("Content-Disposition", *resp.ContentDisposition)
	}

	// 获取 Content-Type 和 Content-Length
	contentType := "application/octet-stream"
	if resp.ContentType != nil && strings.TrimSpace(*resp.ContentType) != "" {
		contentType = *resp.ContentType
	}

	size := int64(-1)
	if resp.ContentLength != nil {
		size = *resp.ContentLength
	}

	return &Object{
		Body:        resp.Body,
		ContentType: contentType,
		Size:        size,
		Headers:     headers,
	}, nil
}
