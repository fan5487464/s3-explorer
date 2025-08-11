package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3" // SQLite 驱动
)

// S3ServiceConfig 定义单个 S3 服务的配置信息
type S3ServiceConfig struct {
	Alias     string `json:"alias"`               // 服务别名，用于显示
	Endpoint  string `json:"endpoint"`            // S3 服务地址，例如："s3.amazonaws.com" 或 "localhost:9000"
	AccessKey string `json:"accessKey"`           // 访问密钥 ID
	SecretKey string `json:"secretKey"`           // 秘密访问密钥
	ViewMode  string `json:"view_mode,omitempty"` // 视图模式 ("list" or "grid")
}

// ConfigStore 存储所有 S3 服务的配置列表
type ConfigStore struct {
	Services []S3ServiceConfig `json:"services"` // S3 服务配置列表
}

var db *sql.DB

// initDB 初始化 SQLite 数据库连接和表
func InitDB() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	appConfigDir := filepath.Join(configDir, "s3-explorer")
	if err := os.MkdirAll(appConfigDir, 0755); err != nil {
		return fmt.Errorf("创建应用配置目录失败: %w", err)
	}
	dbPath := filepath.Join(appConfigDir, "s3-explorer.db")

	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	// 创建 services 表
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS services (
		alias TEXT NOT NULL PRIMARY KEY,
		endpoint TEXT NOT NULL,
		accessKey TEXT NOT NULL,
		secretKey TEXT NOT NULL,
		viewMode TEXT
	);`
	_, err = db.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("创建 services 表失败: %w", err)
	}

	// 检查是否需要从旧的 JSON 文件迁移数据
	jsonFilePath := filepath.Join(appConfigDir, "servers.json")
	if _, err := os.Stat(jsonFilePath); err == nil {
		// JSON 文件存在，检查数据库是否为空
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM services").Scan(&count)
		if err != nil {
			return fmt.Errorf("查询 services 表记录数失败: %w", err)
		}

		if count == 0 {
			log.Println("检测到旧的 servers.json 文件，开始迁移数据...")
			err := migrateFromJSON(jsonFilePath)
			if err != nil {
				return fmt.Errorf("从 JSON 文件迁移数据失败: %w", err)
			}
			log.Println("数据迁移完成，旧的 servers.json 文件将被删除。")
			// 迁移成功后删除旧的 JSON 文件
			if err := os.Remove(jsonFilePath); err != nil {
				log.Printf("删除旧的 servers.json 文件失败: %v", err)
			}
		} else {
			log.Println("检测到旧的 servers.json 文件，但数据库中已有数据，跳过迁移。")
		}
	}

	return nil
}

// migrateFromJSON 从旧的 JSON 文件中读取数据并插入到 SQLite 数据库
func migrateFromJSON(filePath string) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取 JSON 文件失败: %w", err)
	}

	var store ConfigStore
	err = json.Unmarshal(data, &store)
	if err != nil {
		return fmt.Errorf("解析 JSON 数据失败: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback() // 发生错误时回滚

	stmt, err := tx.Prepare("INSERT INTO services (alias, endpoint, accessKey, secretKey, viewMode) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("准备插入语句失败: %w", err)
	}
	defer stmt.Close()

	for _, svc := range store.Services {
		_, err := stmt.Exec(svc.Alias, svc.Endpoint, svc.AccessKey, svc.SecretKey, svc.ViewMode)
		if err != nil {
			// 如果是主键冲突，可能是因为用户手动创建了同名服务，跳过
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				log.Printf("服务 '%s' 已存在于数据库中，跳过插入。", svc.Alias)
				continue
			}
			return fmt.Errorf("插入服务 '%s' 失败: %w", svc.Alias, err)
		}
	}

	return tx.Commit()
}

// LoadConfig 从数据库加载 S3 服务配置
func LoadConfig() (*ConfigStore, error) {
	rows, err := db.Query("SELECT alias, endpoint, accessKey, secretKey, viewMode FROM services")
	if err != nil {
		return nil, fmt.Errorf("查询服务失败: %w", err)
	}
	defer rows.Close()

	var services []S3ServiceConfig
	for rows.Next() {
		var svc S3ServiceConfig
		if err := rows.Scan(&svc.Alias, &svc.Endpoint, &svc.AccessKey, &svc.SecretKey, &svc.ViewMode); err != nil {
			return nil, fmt.Errorf("扫描服务数据失败: %w", err)
		}
		services = append(services, svc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历服务结果集失败: %w", err)
	}

	return &ConfigStore{Services: services}, nil
}

// AddService 添加一个新的 S3 服务配置到数据库
func (cs *ConfigStore) AddService(service S3ServiceConfig) error {
	_, err := db.Exec("INSERT INTO services (alias, endpoint, accessKey, secretKey, viewMode) VALUES (?, ?, ?, ?, ?)",
		service.Alias, service.Endpoint, service.AccessKey, service.SecretKey, service.ViewMode)
	if err != nil {
		return fmt.Errorf("添加服务失败: %w", err)
	}
	return nil
}

// UpdateService 更新一个 S3 服务配置到数据库
func (cs *ConfigStore) UpdateService(oldAlias string, newService S3ServiceConfig) error {
	_, err := db.Exec("UPDATE services SET alias = ?, endpoint = ?, accessKey = ?, secretKey = ?, viewMode = ? WHERE alias = ?",
		newService.Alias, newService.Endpoint, newService.AccessKey, newService.SecretKey, newService.ViewMode, oldAlias)
	if err != nil {
		return fmt.Errorf("更新服务失败: %w", err)
	}
	return nil
}

// DeleteService 从数据库删除一个 S3 服务配置
func (cs *ConfigStore) DeleteService(alias string) error {
	_, err := db.Exec("DELETE FROM services WHERE alias = ?", alias)
	if err != nil {
		return fmt.Errorf("删除服务失败: %w", err)
	}
	return nil
}
