package config

import (
	"encoding/json" // 导入 json 包用于 JSON 序列化和反序列化
	"io/ioutil"     // 导入 ioutil 包用于文件读写
	"log"
	"os"            // 导入 os 包用于文件系统操作
	"path/filepath" // 导入 path/filepath 包用于路径操作
)

// S3ServiceConfig 定义单个 S3 服务的配置信息
type S3ServiceConfig struct {
	Alias     string `json:"alias"`     // 服务别名，用于显示
	Endpoint  string `json:"endpoint"`  // S3 服务地址，例如："s3.amazonaws.com" 或 "localhost:9000"
	AccessKey string `json:"accessKey"` // 访问密钥 ID
	SecretKey string `json:"secretKey"` // 秘密访问密钥
}

// ConfigStore 存储所有 S3 服务的配置列表
type ConfigStore struct {
	Services []S3ServiceConfig `json:"services"` // S3 服务配置列表
}

// configFilePath 返回配置文件的完整路径
func configFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	// 在用户配置目录下创建 s3-explorer 目录
	appConfigDir := filepath.Join(configDir, "s3-explorer")
	// 确保目录存在
	err = os.MkdirAll(appConfigDir, 0755)
	if err != nil {
		return "", err
	}
	log.Println("appConfigDir:", appConfigDir)
	return filepath.Join(appConfigDir, "servers.json"), nil
}

// LoadConfig 从文件中加载 S3 服务配置
func LoadConfig() (*ConfigStore, error) {
	filePath, err := configFilePath()
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		// 如果文件不存在，返回一个空的配置存储，而不是错误
		if os.IsNotExist(err) {
			return &ConfigStore{Services: []S3ServiceConfig{}}, nil
		}
		return nil, err
	}

	var store ConfigStore
	err = json.Unmarshal(data, &store)
	if err != nil {
		return nil, err
	}

	return &store, nil
}

// SaveConfig 将 S3 服务配置保存到文件
func SaveConfig(store *ConfigStore) error {
	filePath, err := configFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(store, "", "  ") // 使用 MarshalIndent 格式化输出，便于阅读
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filePath, data, 0644)
}

// AddService 添加一个新的 S3 服务配置
func (cs *ConfigStore) AddService(service S3ServiceConfig) {
	cs.Services = append(cs.Services, service)
}

// UpdateService 更新一个 S3 服务配置
// 根据别名查找并更新，如果找不到则不执行任何操作
func (cs *ConfigStore) UpdateService(oldAlias string, newService S3ServiceConfig) {
	for i, s := range cs.Services {
		if s.Alias == oldAlias {
			cs.Services[i] = newService
			return
		}
	}
}

// DeleteService 删除一个 S3 服务配置
// 根据别名查找并删除，如果找不到则不执行任何操作
func (cs *ConfigStore) DeleteService(alias string) {
	for i, s := range cs.Services {
		if s.Alias == alias {
			cs.Services = append(cs.Services[:i], cs.Services[i+1:]...)
			return
		}
	}
}
