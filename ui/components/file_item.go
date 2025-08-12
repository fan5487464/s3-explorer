package components

import (
	"fyne.io/fyne/v2"
)

// FileItem 代表一个文件或文件夹项目
type FileItem struct {
	Name         string
	Key          string
	IsFolder     bool
	Size         int64
	LastModified string
}

// ItemEventHandler 处理文件项的事件
type ItemEventHandler interface {
	OnItemSelected(id int, modifiers fyne.KeyModifier)
	OnItemDoubleTapped(id int)
}
