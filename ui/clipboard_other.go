//go:build !windows

package ui

// getFilePathsFromClipboard 在非Windows平台上返回空列表
// 因为非Windows平台可能有不同的剪贴板处理机制
func getFilePathsFromClipboard() ([]string, error) {
	return nil, nil
}