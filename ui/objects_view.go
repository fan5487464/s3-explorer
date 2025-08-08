package ui

import (
	"fmt"
	"fyne.io/fyne/v2/driver/desktop"
	"io"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"s3-explorer/s3client" // 导入 S3 客户端包
)

// listEntry 是一个自定义的列表项组件，用于处理双击和带修饰键的点击
type listEntry struct {
	widget.BaseWidget
	icon      *widget.Icon
	nameLabel *widget.Label
	infoLabel *widget.Label

	id widget.ListItemID
	ov *ObjectsView // 指向父视图的引用

	doubleTapped func()
}

func (e *listEntry) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewHBox(
		e.icon,
		e.nameLabel,
		layout.NewSpacer(),
		e.infoLabel,
	))
}

// DoubleTapped 实现了 fyne.DoubleTappable 接口
func (e *listEntry) DoubleTapped(_ *fyne.PointEvent) {
	if e.doubleTapped != nil {
		e.doubleTapped()
	}
}

// MouseDown 实现了 desktop.Mouseable 接口，用于捕获带有修饰键的点击
func (e *listEntry) MouseDown(m *desktop.MouseEvent) {
	e.ov.handleItemClick(e.id, m)
}

func (e *listEntry) MouseUp(_ *desktop.MouseEvent) {}

// newListEntry 创建一个新的 listEntry 实例
func newListEntry(ov *ObjectsView) *listEntry {
	entry := &listEntry{
		icon:      widget.NewIcon(theme.FileIcon()),
		nameLabel: widget.NewLabel("Name"),
		infoLabel: widget.NewLabel("Size/Time"),
		ov:        ov,
	}
	entry.ExtendBaseWidget(entry)
	return entry
}

// ObjectsView 结构体用于管理右侧的文件/文件夹列表视图
type ObjectsView struct {
	window              fyne.Window
	s3Client            *s3client.S3Client
	currentBucket       string
	currentPrefix       string // 当前路径，例如 "folder1/subfolder/"
	objects             []s3client.S3Object
	objectList          *widget.List                // 用于显示文件/文件夹列表的 Fyne 列表组件
	breadcrumbContainer *fyne.Container             // 面包屑容器
	selectedObjectIDs   map[widget.ListItemID]struct{} // 存储所有选中的对象 ID
	lastSelectedID      widget.ListItemID           // 存储最后一次单击的对象 ID，用于 shift 多选
	loadingIndicator    *widget.ProgressBarInfinite // 加载指示器
	downloadButton      *widget.Button
	deleteButton        *widget.Button
}

// NewObjectsView 创建并返回一个新的 ObjectsView 实例
func NewObjectsView(w fyne.Window) *ObjectsView {
	ov := &ObjectsView{
		window:            w,
		selectedObjectIDs: make(map[widget.ListItemID]struct{}),
		lastSelectedID:    -1, // 初始状态为未选中
		loadingIndicator:  widget.NewProgressBarInfinite(), // 初始化加载指示器
	}
	ov.loadingIndicator.Hide() // 默认隐藏
	return ov
}

// SetBucketAndPrefix 设置当前存储桶和前缀，并加载对象列表
func (ov *ObjectsView) SetBucketAndPrefix(client *s3client.S3Client, bucket, prefix string) {
	ov.s3Client = client
	ov.currentBucket = bucket
	ov.currentPrefix = prefix

	// 重置选择状态
	if ov.objectList != nil {
		ov.objectList.UnselectAll()
	}
	ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
	ov.lastSelectedID = -1
	ov.updateButtonsState()

	ov.loadObjects()
	ov.updateBreadcrumbs() // 更新面包屑
}

// loadObjects 加载指定存储桶和前缀下的对象列表
func (ov *ObjectsView) loadObjects() {
	if ov.s3Client == nil || ov.currentBucket == "" {
		ov.objects = []s3client.S3Object{} // 没有客户端或未选择存储桶，清空列表
		ov.refreshObjectList()
		ov.updateButtonsState()
		return
	}

	ov.loadingIndicator.Show() // 显示加载指示器
	go func() {
		objects, err := ov.s3Client.ListObjects(ov.currentBucket, ov.currentPrefix)
		fyne.Do(func() {
			ov.loadingIndicator.Hide() // 隐藏加载指示器
			if err != nil {
				log.Printf("列出对象失败: %v", err)
				dialog.ShowError(fmt.Errorf("列出对象失败: %v", err), ov.window)
				ov.objects = []s3client.S3Object{} // 加载失败，清空列表
			} else {
				ov.objects = objects
			}
			ov.refreshObjectList()
			ov.updateButtonsState()
		})
	}()
}

// refreshObjectList 刷新文件/文件夹列表显示
func (ov *ObjectsView) refreshObjectList() {
	if ov.objectList == nil {
		return
	}
	ov.objectList.Refresh()
}

// updateBreadcrumbs 更新面包屑导航
func (ov *ObjectsView) updateBreadcrumbs() {
	if ov.breadcrumbContainer == nil {
		return
	}

	ov.breadcrumbContainer.RemoveAll() // 清空现有面包屑

	// 添加根目录 (存储桶名称)
	if ov.currentBucket != "" {
		bucketBtn := widget.NewButton(ov.currentBucket, func() {
			ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, "") // 返回存储桶根目录
		})
		ov.breadcrumbContainer.Add(bucketBtn)
	}

	// 如果有前缀，添加路径段
	if ov.currentPrefix != "" {
		pathSegments := strings.Split(strings.TrimSuffix(ov.currentPrefix, "/"), "/")
		currentPath := ""
		for _, segment := range pathSegments {
			if segment == "" { // 避免空段
				continue
			}
			currentPath += segment + "/"
			// 闭包捕获当前路径，确保点击时使用正确的路径
			pathForClosure := currentPath // 复制变量，避免闭包问题
			segmentBtn := widget.NewButton(segment, func() {
				ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, pathForClosure)
			})
			ov.breadcrumbContainer.Add(widget.NewLabel(">")) // 分隔符
			ov.breadcrumbContainer.Add(segmentBtn)
		}
	}
	ov.breadcrumbContainer.Refresh()
}

// handleItemClick 处理列表项的点击事件，包含多选逻辑
func (ov *ObjectsView) handleItemClick(id widget.ListItemID, m *desktop.MouseEvent) {
	if m.Button == desktop.MouseButtonSecondary {
		// 未来可以实现右键菜单
		return
	}

	ctrl := m.Modifier&desktop.ControlModifier != 0 || m.Modifier&desktop.SuperModifier != 0
	shift := m.Modifier&desktop.ShiftModifier != 0

	if !ctrl && !shift {
		// 普通单击：清空现有选择，并选中当前项
		ov.objectList.UnselectAll()
		ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})

		ov.objectList.Select(id)
		ov.selectedObjectIDs[id] = struct{}{}
		ov.lastSelectedID = id
	} else if ctrl {
		// Ctrl/Cmd + 单击：切换选中状态
		if _, selected := ov.selectedObjectIDs[id]; selected {
			ov.objectList.Unselect(id)
			delete(ov.selectedObjectIDs, id)
		} else {
			ov.objectList.Select(id)
			ov.selectedObjectIDs[id] = struct{}{}
		}
		ov.lastSelectedID = id
	} else if shift {
		// Shift + 单击：范围选择
		if ov.lastSelectedID == -1 {
			// 如果没有前一个选中的，就当做普通单击
			ov.objectList.Select(id)
			ov.selectedObjectIDs[id] = struct{}{}
			ov.lastSelectedID = id
		} else {
			start, end := ov.lastSelectedID, id
			if start > end {
				start, end = end, start
			}
			for i := start; i <= end; i++ {
				if _, selected := ov.selectedObjectIDs[i]; !selected {
					ov.objectList.Select(i)
					ov.selectedObjectIDs[i] = struct{}{}
				}
			}
		}
	}
	ov.updateButtonsState()
}

// updateButtonsState 根据当前选择状态更新按钮的可用性
func (ov *ObjectsView) updateButtonsState() {
	if ov.downloadButton == nil || ov.deleteButton == nil {
		return
	}

	numSelected := len(ov.selectedObjectIDs)

	// 删除按钮：只要有选中项就启用
	if numSelected > 0 {
		ov.deleteButton.Enable()
	} else {
		ov.deleteButton.Disable()
	}

	// 下载按钮：仅当选中一个文件时启用
	if numSelected == 1 {
		var selectedID widget.ListItemID
		for id := range ov.selectedObjectIDs {
			selectedID = id // 获取唯一的选中ID
		}

		if selectedID < len(ov.objects) && !ov.objects[selectedID].IsFolder {
			ov.downloadButton.Enable()
		} else {
			ov.downloadButton.Disable()
		}
	} else {
		ov.downloadButton.Disable()
	}
}

// GetContent 返回 ObjectsView 的 Fyne UI 内容
func (ov *ObjectsView) GetContent() fyne.CanvasObject {
	ov.objectList = widget.NewList(
		func() int {
			return len(ov.objects)
		},
		func() fyne.CanvasObject {
			// 列表项模板使用我们自定义的 listEntry
			return newListEntry(ov)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// 更新列表项内容
			item := ov.objects[id]
			entry := obj.(*listEntry)

			entry.id = id
			entry.nameLabel.SetText(item.Name)
			if item.IsFolder {
				entry.icon.SetResource(theme.FolderIcon())
				entry.infoLabel.SetText("文件夹")
				// 设置双击事件
				entry.doubleTapped = func() {
					newPrefix := ov.currentPrefix + item.Name + "/"
					ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, newPrefix)
				}
			} else {
				entry.icon.SetResource(theme.FileIcon())
				entry.infoLabel.SetText(fmt.Sprintf("%s | %s", formatBytes(item.Size), item.LastModified))
				// 文件没有双击事件
				entry.doubleTapped = nil
			}
		},
	)

	// 面包屑容器
	ov.breadcrumbContainer = container.NewHBox()
	ov.updateBreadcrumbs() // 初始化面包屑

	// 上传按钮
	uploadButton := widget.NewButtonWithIcon("上传", theme.ContentAddIcon(), func() {
		if ov.s3Client == nil || ov.currentBucket == "" {
			dialog.ShowInformation("提示", "请先选择一个 S3 服务和存储桶。", ov.window)
			return
		}

		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, ov.window)
				return
			}
			if reader == nil {
				return
			}
			defer reader.Close()

			filePath := reader.URI().Path()
			fileName := filepath.Base(filePath)
			s3Key := ov.currentPrefix + fileName

			data, err := ioutil.ReadAll(reader)
			if err != nil {
				dialog.ShowError(fmt.Errorf("读取文件失败: %v", err), ov.window)
				return
			}

			go func() {
				err := ov.s3Client.UploadObject(ov.currentBucket, s3Key, strings.NewReader(string(data)), int64(len(data)))
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("上传失败: %v", err), ov.window)
					} else {
						dialog.ShowInformation("成功", fmt.Sprintf("文件 %s 上传成功！", fileName), ov.window)
						ov.loadObjects()
					}
				})
			}()
		}, ov.window)
		fd.Show()
	})

	// 下载按钮
	ov.downloadButton = widget.NewButtonWithIcon("下载", theme.DownloadIcon(), func() {
		if len(ov.selectedObjectIDs) != 1 {
			dialog.ShowInformation("提示", "请选择一个文件进行下载。", ov.window)
			return
		}
		var selectedID widget.ListItemID
		for id := range ov.selectedObjectIDs {
			selectedID = id
		}

		selectedObject := ov.objects[selectedID]
		if selectedObject.IsFolder {
			dialog.ShowInformation("提示", "无法下载文件夹。", ov.window)
			return
		}

		fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, ov.window)
				return
			}
			if writer == nil {
				return
			}

			s3Key := ov.currentPrefix + selectedObject.Name

			go func() {
				body, err := ov.s3Client.DownloadObject(ov.currentBucket, s3Key)
				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("下载失败: %v", err), ov.window)
					})
					return
				}
				defer body.Close()

				_, err = io.Copy(writer, body)
				closeErr := writer.Close()
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("保存文件失败: %v", err), ov.window)
					} else if closeErr != nil {
						dialog.ShowError(fmt.Errorf("关闭文件失败: %v", closeErr), ov.window)
					} else {
						dialog.ShowInformation("成功", fmt.Sprintf("文件 %s 下载成功！", selectedObject.Name), ov.window)
					}
				})
			}()
		}, ov.window)
		fd.SetFileName(selectedObject.Name)
		fd.Show()
	})

	// 删除按钮
	ov.deleteButton = widget.NewButtonWithIcon("删除", theme.DeleteIcon(), func() {
		selectedCount := len(ov.selectedObjectIDs)
		if selectedCount == 0 {
			dialog.ShowInformation("提示", "请先选择要删除的文件或文件夹。", ov.window)
			return
		}

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除选中的 %d 个项目吗？", selectedCount), func(confirmed bool) {
			if confirmed {
				go func() {
					var wg sync.WaitGroup
					var mu sync.Mutex
					var failedDeletions []string

					for id := range ov.selectedObjectIDs {
						if id >= len(ov.objects) {
							continue
						}
						wg.Add(1)
						go func(selectedObject s3client.S3Object) {
							defer wg.Done()
							s3Key := ov.currentPrefix + selectedObject.Name
							if selectedObject.IsFolder {
								// S3 删除"文件夹"通常是删除以该前缀命名的0字节对象
								// 但更稳妥的做法是确保key以/结尾，如果ListObjects返回的就是这样的
								// 我们的ListObjects返回的是不带/的文件夹名，所以这里要加上
								s3Key += "/"
							}
							err := ov.s3Client.DeleteObject(ov.currentBucket, s3Key)
							if err != nil {
								mu.Lock()
								failedDeletions = append(failedDeletions, selectedObject.Name)
								mu.Unlock()
								log.Printf("删除对象 %s 失败: %v", s3Key, err)
							}
						}(ov.objects[id])
					}
					wg.Wait()

					fyne.Do(func() {
						if len(failedDeletions) > 0 {
							dialog.ShowError(fmt.Errorf("部分项目删除失败: %s", strings.Join(failedDeletions, ", ")), ov.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("%d 个项目已删除。", selectedCount), ov.window)
						}
						ov.loadObjects() // 刷新列表
					})
				}()
			}
		}, ov.window)
	})

	ov.updateButtonsState() // 初始化按钮状态

	// 文件操作按钮布局
	fileOpsButtons := container.NewHBox(uploadButton, ov.downloadButton, ov.deleteButton)

	// 顶部操作栏：面包屑 + 加载指示器 + 操作按钮
	topBar := container.NewBorder(nil, nil,
		ov.breadcrumbContainer,
		container.NewHBox(layout.NewSpacer(), ov.loadingIndicator, fileOpsButtons),
		nil,
	)

	// 整体布局：顶部操作栏 + 分隔符 + 文件列表
	return container.NewBorder(topBar, nil, nil, nil, container.NewVBox(widget.NewSeparator()), ov.objectList)
}

// formatBytes 格式化字节大小为可读的字符串
func formatBytes(b int64) string {
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