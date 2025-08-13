//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	openClipboard        = user32.NewProc("OpenClipboard")
	closeClipboard       = user32.NewProc("CloseClipboard")
	getClipboardData     = user32.NewProc("GetClipboardData")
	
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	globalLock           = kernel32.NewProc("GlobalLock")
	globalUnlock         = kernel32.NewProc("GlobalUnlock")
)

const (
	CF_HDROP = 15
)

// DROPFILES 结构体
type DROPFILES struct {
	pFiles uintptr
	pt     struct{ x, y int32 }
	fNC    int32
	fWide  int32
}

// getFilePathsFromClipboard 从Windows剪贴板读取文件路径
func getFilePathsFromClipboard() ([]string, error) {
	// 打开剪贴板
	ret, _, err := openClipboard.Call(0)
	if ret == 0 {
		return nil, err
	}
	defer closeClipboard.Call()
	
	// 检查CF_HDROP格式是否可用
	ret, _, _ = isClipboardFormatAvailable.Call(CF_HDROP)
	if ret == 0 {
		return nil, nil // 格式不可用，返回空列表而不是错误
	}
	
	// 获取CF_HDROP数据
	hDrop, _, _ := getClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, nil // 无法获取数据，返回空列表
	}
	
	// 锁定全局内存
	dropFilesPtr, _, _ := globalLock.Call(hDrop)
	if dropFilesPtr == 0 {
		return nil, nil // 无法锁定内存，返回空列表
	}
	defer globalUnlock.Call(hDrop)
	
	// 解析DROPFILES结构
	dropFiles := (*DROPFILES)(unsafe.Pointer(dropFilesPtr))
	
	// 获取文件列表的起始位置
	filesStart := dropFilesPtr + dropFiles.pFiles
	
	// 读取文件路径
	var filePaths []string
	if dropFiles.fWide != 0 {
		// Unicode格式
		ptr := (*uint16)(unsafe.Pointer(filesStart))
		for {
			// 读取UTF-16字符串
			var chars []uint16
			for {
				char := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(len(chars)*2)))
				if char == 0 {
					break
				}
				chars = append(chars, char)
			}
			
			// 如果是空字符串，表示结束
			if len(chars) == 0 {
				break
			}
			
			// 转换为UTF-8字符串
			filePath := syscall.UTF16ToString(chars)
			filePaths = append(filePaths, filePath)
			
			// 移动到下一个字符串（包括终止符）
			ptr = (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr((len(chars)+1)*2)))
		}
	} else {
		// ANSI格式
		ptr := (*byte)(unsafe.Pointer(filesStart))
		for {
			// 读取ANSI字符串
			var chars []byte
			for {
				char := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(len(chars))))
				if char == 0 {
					break
				}
				chars = append(chars, char)
			}
			
			// 如果是空字符串，表示结束
			if len(chars) == 0 {
				break
			}
			
			// 转换为UTF-8字符串
			filePath := string(chars)
			filePaths = append(filePaths, filePath)
			
			// 移动到下一个字符串（包括终止符）
			ptr = (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(len(chars)+1)))
		}
	}
	
	return filePaths, nil
}