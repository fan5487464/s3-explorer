package common

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FormatBytes 格式化字节大小为可读的字符串
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// GetIconForFile 根据文件名返回对应的图标类型
func GetIconForFile(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".svg", ".webp":
		return "image"
	case ".mp3", ".wav", ".ogg", ".flac":
		return "audio"
	case ".mp4", ".avi", ".mov", ".mkv", ".webm":
		return "video"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2":
		return "archive"
	case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".cfg":
		return "text"
	default:
		return "file"
	}
}

// IsPreviewableImage 检查文件是否为可预览的图片
func IsPreviewableImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	default:
		return false
	}
}

// FormatFileNameForDisplay 格式化文件名，确保单行显示，过长则截断并保留后缀
func FormatFileNameForDisplay(fileName string, maxDisplayLength int) string {
	ext := filepath.Ext(fileName)
	baseName := strings.TrimSuffix(fileName, ext)

	// 计算去除"..."和扩展名后，基本名称的可用长度
	availableBaseLen := maxDisplayLength - 3 - len(ext) // 3 个字符是 "..."

	if availableBaseLen < 0 {
		if len(fileName) > maxDisplayLength {
			return fileName[:maxDisplayLength-3] + "..."
		}
		return fileName
	}

	if len(baseName) > availableBaseLen {
		return baseName[:availableBaseLen] + "..." + ext
	}
	return fileName
}