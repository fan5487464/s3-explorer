package s3client

import (
	"context"
	"errors"
	"fmt"
	"io" // 导入 io 包
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

	// 如果配置了代理，则创建一个带有代理的 HTTP 客户端
	if svcConfig.Proxy != "" {
		proxyURL, err := url.Parse(svcConfig.Proxy)
		if err != nil {
			return nil, fmt.Errorf("解析代理 URL 失败: %w", err)
		}
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
		cfg.HTTPClient = &http.Client{
			Transport: transport,
		}
	}

	// 创建 S3 客户端，并启用路径风格访问
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true // 修正：启用路径风格访问，对于 Minio 等 S3 兼容服务很重要
		// 显式设置校验和计算和验证策略为 Unset，以避免与 HTTP 和非 seekable streams 相关的问题
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationUnset
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationUnset
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
	Name         string // 对象的简称 (例如 "file.txt" 或 "subfolder")
	Key          string // 对象的完整 S3 Key
	IsFolder     bool   // 是否是文件夹
	Size         int64  // 文件大小 (字节)
	LastModified string // 最后修改时间
}

// ListObjects 列出指定存储桶和前缀下的对象（分页）
// 此方法会优先显示文件夹，然后再显示文件
func (sc *S3Client) ListObjects(bucketName, prefix, marker string, pageSize int32) ([]S3Object, *string, error) {
	// 如果 marker 为空，说明是第一页，我们需要获取所有对象然后重新分页
	if marker == "" {
		// 获取所有对象
		allObjects, err := sc.ListAllObjectsUnderPrefix(bucketName, prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("列出所有对象失败: %w", err)
		}
		
		// 分离文件夹和文件
		var folders, files []S3Object
		for _, obj := range allObjects {
			if obj.IsFolder {
				folders = append(folders, obj)
			} else {
				files = append(files, obj)
			}
		}
		
		// 合并文件夹和文件（文件夹在前）
		allObjects = append(folders, files...)
		
		// 如果对象数量小于等于页面大小，直接返回所有对象
		if int32(len(allObjects)) <= pageSize {
			return allObjects, nil, nil
		}
		
		// 返回第一页数据
		nextMarker := fmt.Sprintf("page_2")
		return allObjects[:pageSize], &nextMarker, nil
	} else {
		// 获取所有对象
		allObjects, err := sc.ListAllObjectsUnderPrefix(bucketName, prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("列出所有对象失败: %w", err)
		}
		
		// 分离文件夹和文件
		var folders, files []S3Object
		for _, obj := range allObjects {
			if obj.IsFolder {
				folders = append(folders, obj)
			} else {
				files = append(files, obj)
			}
		}
		
		// 合并文件夹和文件（文件夹在前）
		allObjects = append(folders, files...)
		
		// 解析页码
		var page int
		fmt.Sscanf(marker, "page_%d", &page)
		
		// 计算起始索引
		startIndex := int32((page - 1)) * pageSize
		
		// 如果起始索引超出范围，返回空
		if startIndex >= int32(len(allObjects)) {
			return []S3Object{}, nil, nil
		}
		
		// 计算结束索引
		endIndex := startIndex + pageSize
		if endIndex > int32(len(allObjects)) {
			endIndex = int32(len(allObjects))
		}
		
		// 获取当前页数据
		pageObjects := allObjects[startIndex:endIndex]
		
		// 如果还有更多页面，设置下一个标记
		var nextMarker *string
		if endIndex < int32(len(allObjects)) {
			nextMarkerStr := fmt.Sprintf("page_%d", page+1)
			nextMarker = &nextMarkerStr
		}
		
		return pageObjects, nextMarker, nil
	}
}

// UploadObject 上传文件到 S3
func (sc *S3Client) UploadObject(bucketName, key string, reader io.Reader, size int64) error {
	_, err := sc.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(bucketName),
		Key:           aws.String(key),
		Body:          reader,
		ContentLength: &size,
		// 移除了 ChecksumAlgorithm 字段，让 SDK 使用默认行为
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

// CreateFolder 在 S3 中创建一个文件夹（即一个以 / 结尾的 0 字节对象）
func (sc *S3Client) CreateFolder(bucketName, key string) error {
	// 确保 key 以 / 结尾
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}

	_, err := sc.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader(""), // 空内容
	})

	if err != nil {
		return fmt.Errorf("创建文件夹失败: %w", err)
	}
	return nil
}

// ListAllObjectsUnderPrefix 递归地列出指定前缀下的所有对象（包括文件和文件夹）
func (sc *S3Client) ListAllObjectsUnderPrefix(bucketName, prefix string) ([]S3Object, error) {
	var objects []S3Object
	paginator := s3.NewListObjectsV2Paginator(sc.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucketName),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"), // 添加分隔符以识别文件夹
	})

	processedKeys := make(map[string]bool) // 用于跟踪已处理的键，避免重复

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("列出对象失败: %w", err)
		}

		// 处理 CommonPrefixes (文件夹)
		for _, commonPrefix := range page.CommonPrefixes {
			fullKey := *commonPrefix.Prefix
			// 避免重复处理
			if processedKeys[fullKey] {
				continue
			}
			processedKeys[fullKey] = true

			name := strings.TrimSuffix(fullKey, "/")
			if prefix != "" {
				name = strings.TrimPrefix(name, prefix)
			}

			objects = append(objects, S3Object{
				Name:     name,
				Key:      fullKey,
				IsFolder: true,
			})
		}

		// 处理 Contents (文件)
		for _, content := range page.Contents {
			fullKey := *content.Key
			// 避免重复处理
			if processedKeys[fullKey] {
				continue
			}
			processedKeys[fullKey] = true

			// 忽略 S3 中的"文件夹"占位符对象（key 以 / 结尾且大小为 0）
			if strings.HasSuffix(fullKey, "/") && *content.Size == 0 {
				continue
			}

			// 提取文件名，去除前缀
			fileName := strings.TrimPrefix(fullKey, prefix)
			objects = append(objects, S3Object{
				Name:         fileName,
				Key:          fullKey,
				IsFolder:     false,
				Size:         *content.Size,
				LastModified: content.LastModified.Format("2006-01-02 15:04:05"),
			})
		}
	}

	// 对对象进行排序，将文件夹放在前面，然后按名称排序
	sort.Slice(objects, func(i, j int) bool {
		// 如果一个是文件夹，另一个是文件，则文件夹排在前面
		if objects[i].IsFolder && !objects[j].IsFolder {
			return true
		}
		if !objects[i].IsFolder && objects[j].IsFolder {
			return false
		}
		// 如果两个都是文件夹或都是文件，则按名称排序
		return objects[i].Name < objects[j].Name
	})

	return objects, nil
}

// ListAllKeysUnderPrefix 递归地列出指定前缀下的所有对象键（文件和文件夹标记）。
func (sc *S3Client) ListAllKeysUnderPrefix(bucketName, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(sc.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("列出对象键失败: %w", err)
		}

		for _, content := range page.Contents {
			keys = append(keys, *content.Key)
		}
	}
	return keys, nil
}

// CopyObject 在同一个存储桶内复制对象
func (sc *S3Client) CopyObject(bucketName, sourceKey, targetKey string) error {
	// 构建源对象的完整路径
	source := fmt.Sprintf("%s/%s", bucketName, sourceKey)
	
	_, err := sc.client.CopyObject(context.TODO(), &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		CopySource: aws.String(source),
		Key:        aws.String(targetKey),
	})
	
	if err != nil {
		return fmt.Errorf("复制对象失败: %w", err)
	}
	return nil
}

// ObjectExists 检查对象是否存在于存储桶中
func (sc *S3Client) ObjectExists(bucketName, key string) (bool, error) {
	// 如果键为空，直接返回false
	if key == "" {
		return false, nil
	}
	
	_, err := sc.client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	
	if err != nil {
		// 检查是否是因为对象不存在导致的错误
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return false, nil // 对象不存在，但不是错误
		}
		
		// 检查是否包含404错误
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "NotFound") {
			return false, nil // 对象不存在，但不是错误
		}
		
		// 检查是否包含400错误
		if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "BadRequest") {
			// 400错误通常意味着键格式不正确，我们也认为对象不存在
			return false, nil
		}
		
		return false, fmt.Errorf("检查对象是否存在时出错: %w", err)
	}
	
	return true, nil // 对象存在
}
