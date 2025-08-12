package ui

import (
	"fmt"
	"io"
	"sync/atomic"

	"fyne.io/fyne/v2" // Added fyne import
	"fyne.io/fyne/v2/dialog"
)

// ProgressTracker 包装一个 io.Reader 以跟踪读取进度并更新进度条。
// 如果底层 reader 也是 io.ReadSeeker，则 ProgressTracker 也将实现 io.ReadSeeker。
type ProgressTracker struct {
	reader              io.Reader
	seeker              io.ReadSeeker // 如果 reader 可寻址则保存 seeker
	totalSize           int64
	bytesTransferred    *int64                 // 使用指针指向原子计数器以共享进度
	totalProgressDialog *dialog.ProgressDialog // 更改为 *dialog.ProgressDialog
	totalProgressValue  *float64               // 使用指针以共享进度值
}

// NewProgressTracker 为单个读取操作创建一个新的进度跟踪器
// 该操作将贡献于一个总进度条。
func NewProgressTracker(
	reader io.Reader,
	totalSize int64,
	bytesTransferred *int64,
	totalProgressDialog *dialog.ProgressDialog,
) *ProgressTracker {
	// 尝试类型断言，看 reader 是否也是 io.ReadSeeker
	seeker, _ := reader.(io.ReadSeeker) // 如果失败我们不关心，seeker 将为 nil

	return &ProgressTracker{
		reader:              reader,
		seeker:              seeker, // 存储 seeker (可能为 nil)
		totalSize:           totalSize,
		bytesTransferred:    bytesTransferred,
		totalProgressDialog: totalProgressDialog,
	}
}

// Read 实现了 io.Reader 接口。
func (p *ProgressTracker) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	if n > 0 {
		// 原子性地将读取的字节数加到总数中。
		newVal := atomic.AddInt64(p.bytesTransferred, int64(n))

		// 更新进度条。
		if p.totalSize > 0 {
			progress := float64(newVal) / float64(p.totalSize)
			if p.totalProgressDialog != nil {
				fyne.Do(func() {
					p.totalProgressDialog.SetValue(progress)
				})
			}
		}
	}
	return n, err
}

// Seek 如果底层 reader 是可寻址的，则实现 io.Seeker 接口。
func (p *ProgressTracker) Seek(offset int64, whence int) (int64, error) {
	if p.seeker != nil {
		return p.seeker.Seek(offset, whence)
	}
	// 如果底层 reader 不可寻址，则返回错误
	return 0, fmt.Errorf("底层 reader 不支持 seek 操作")
}

// ProgressWriter 包装一个 io.Writer 以跟踪写入进度并更新进度条。
type ProgressWriter struct {
	writer              io.Writer
	totalSize           int64
	bytesTransferred    *int64 // 指向共享原子计数器的指针
	totalProgressDialog *dialog.ProgressDialog
}

// NewProgressWriter 为写入操作创建一个新的进度跟踪器。
func NewProgressWriter(
	writer io.Writer,
	totalSize int64,
	bytesTransferred *int64,
	progressDialog *dialog.ProgressDialog,
) *ProgressWriter {
	return &ProgressWriter{
		writer:              writer,
		totalSize:           totalSize,
		bytesTransferred:    bytesTransferred,
		totalProgressDialog: progressDialog,
	}
}

// Write 实现了 io.Writer 接口。
func (p *ProgressWriter) Write(b []byte) (int, error) {
	n, err := p.writer.Write(b)
	if n > 0 {
		newVal := atomic.AddInt64(p.bytesTransferred, int64(n))
		if p.totalSize > 0 {
			progress := float64(newVal) / float64(p.totalSize)
			if p.totalProgressDialog != nil {
				fyne.Do(func() {
					p.totalProgressDialog.SetValue(progress)
				})
			}
		}
	}
	return n, err
}