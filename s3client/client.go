package s3client

import (
	"context"
	"fmt"
	"io" // 导入 io 包
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	appConfig "s3-explorer/config" // 导入应用程序的配置包
)

// S3Client 结构体封装了 AWS S3 客户端
type S3Client struct {
	client *s3.Client
}

// NewS3Client 根据 S3 服务配置创建一个新的 S3Client 实例
func NewS3Client(svcConfig appConfig.S3ServiceConfig) (*S3Client, error) {
	// 构建自定义解析器，用于支持 Minio 等自定义 Endpoint
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if svcConfig.Endpoint != "" {
			return aws.Endpoint{
					URL:           svcConfig.Endpoint,       // 修正：使用 URL 字段
					PartitionID:   "aws",                    // 通常为 "aws"，或根据实际服务提供商设置
					SigningRegion: region,                   // 使用传入的区域进行签名
					Source:        aws.EndpointSourceCustom, // 标记为自定义 Endpoint
				},
				nil
		}
		// 如果没有自定义 Endpoint，则使用默认解析器
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	cfg, err := config.LoadDefaultConfig( // 修正：使用 LoadDefaultConfig
		context.TODO(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(svcConfig.AccessKey, svcConfig.SecretKey, "")),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithRegion("us-east-1"), // 即使使用自定义 Endpoint，也通常需要指定一个区域
	)
	if err != nil {
		return nil, fmt.Errorf("加载 AWS 配置失败: %w", err)
	}

	// 创建 S3 客户端，并启用路径风格访问
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true // 修正：启用路径风格访问，对于 Minio 等 S3 兼容服务很重要
	})
	return &S3Client{client: client},
		nil
}

// ListBuckets 列出所有存储桶
func (sc *S3Client) ListBuckets() ([]string, error) {
	output, err := sc.client.ListBuckets(context.TODO(), &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("列出存储桶失败: %w", err)
	}

	var buckets []string
	for _, bucket := range output.Buckets {
		buckets = append(buckets, *bucket.Name)
	}
	return buckets, nil
}

// S3Object 表示 S3 中的一个对象（文件或文件夹）
type S3Object struct {
	Name         string // 对象名称
	IsFolder     bool   // 是否是文件夹
	Size         int64  // 文件大小 (字节)
	LastModified string // 最后修改时间
}

// ListObjects 列出指定存储桶和前缀下的对象
func (sc *S3Client) ListObjects(bucketName, prefix string) ([]S3Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucketName),
		Delimiter: aws.String("/"), // 用于区分文件夹
		Prefix:    aws.String(prefix),
	}

	output, err := sc.client.ListObjectsV2(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("列出对象失败: %w", err)
	}

	var objects []S3Object

	// 处理 CommonPrefixes (文件夹)
	for _, commonPrefix := range output.CommonPrefixes {
		folderName := strings.TrimSuffix(*commonPrefix.Prefix, "/")
		objects = append(objects, S3Object{
			Name:     folderName,
			IsFolder: true,
		})
	}

	// 处理 Contents (文件)
	for _, content := range output.Contents {
		// 排除当前前缀本身（如果它是一个文件）
		if *content.Key == prefix {
			continue
		}
		// 提取文件名，去除前缀
		fileName := strings.TrimPrefix(*content.Key, prefix)
		objects = append(objects, S3Object{
			Name:         fileName,
			IsFolder:     false,
			Size:         *content.Size,
			LastModified: content.LastModified.Format("2006-01-02 15:04:05"), // 格式化时间
		})
	}

	return objects, nil
}

// UploadObject 上传文件到 S3
func (sc *S3Client) UploadObject(bucketName, key string, reader io.Reader, size int64) error {
	_, err := sc.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(bucketName),
		Key:           aws.String(key),
		Body:          reader,
		ContentLength: &size,
	})
	if err != nil {
		return fmt.Errorf("上传文件失败: %w", err)
	}
	return nil
}

// DownloadObject 从 S3 下载文件
func (sc *S3Client) DownloadObject(bucketName, key string) (io.ReadCloser, error) {
	output, err := sc.client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("下载文件失败: %w", err)
	}
	return output.Body, nil
}

// DeleteObject 从 S3 删除对象 (文件或空文件夹) 或空文件夹
func (sc *S3Client) DeleteObject(bucketName, key string) error {
	_, err := sc.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("删除对象失败: %w", err)
	}
	return nil
}

// CreateBucket 创建存储桶
func (sc *S3Client) CreateBucket(bucketName string) error {
	_, err := sc.client.CreateBucket(context.TODO(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("创建存储桶失败: %w", err)
	}
	return nil
}

// DeleteBucket 删除存储桶
func (sc *S3Client) DeleteBucket(bucketName string) error {
	_, err := sc.client.DeleteBucket(context.TODO(), &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("删除存储桶失败: %w", err)
	}
	return nil
}

// IsBucketEmpty 检查存储桶是否为空
func (sc *S3Client) IsBucketEmpty(bucketName string) (bool, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int32(1), // 只请求一个对象，用于判断是否为空
	}
	output, err := sc.client.ListObjectsV2(context.TODO(), input)
	if err != nil {
		return false, fmt.Errorf("检查存储桶是否为空失败: %w", err)
	}
	return len(output.Contents) == 0 && len(output.CommonPrefixes) == 0, nil
}
