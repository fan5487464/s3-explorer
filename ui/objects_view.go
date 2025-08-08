package ui

import (
	"fmt"
	"io"        // 导入 io 包
	"io/ioutil" // 用于文件读写
	"log"
	"path/filepath" // 用于路径操作
	"strings"       // 用于字符串操作

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"s3-explorer/s3client" // 导入 S3 客户端包
)

// ObjectsView 结构体用于管理右侧的文件/文件夹列表视图
type ObjectsView struct {
	window              fyne.Window
	s3Client            *s3client.S3Client
	currentBucket       string
	currentPrefix       string // 当前路径，例如 "folder1/subfolder/"
	objects             []s3client.S3Object
	objectList          *widget.List                // 用于显示文件/文件夹列表的 Fyne 列表组件
	breadcrumbContainer *fyne.Container             // 面包屑容器
	selectedObjectID    widget.ListItemID           // 存储当前选中的对象 ID
	loadingIndicator    *widget.ProgressBarInfinite // 加载指示器
}

// NewObjectsView 创建并返回一个新的 ObjectsView 实例
func NewObjectsView(w fyne.Window) *ObjectsView {
	ov := &ObjectsView{
		window:           w,
		selectedObjectID: -1,                              // 初始状态为未选中
		loadingIndicator: widget.NewProgressBarInfinite(), // 初始化加载指示器
	}
	ov.loadingIndicator.Hide() // 默认隐藏
	return ov
}

// SetBucketAndPrefix 设置当前存储桶和前缀，并加载对象列表
func (ov *ObjectsView) SetBucketAndPrefix(client *s3client.S3Client, bucket, prefix string) {
	ov.s3Client = client
	ov.currentBucket = bucket
	ov.currentPrefix = prefix
	ov.loadObjects()
	ov.updateBreadcrumbs() // 更新面包屑
}

// loadObjects 加载指定存储桶和前缀下的对象列表
func (ov *ObjectsView) loadObjects() {
	if ov.s3Client == nil || ov.currentBucket == "" {
		ov.objects = []s3client.S3Object{} // 没有客户端或未选择存储桶，清空列表
		ov.refreshObjectList()
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
	bucketBtn := widget.NewButton(ov.currentBucket, func() {
		ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, "") // 返回存储桶根目录
	})
	ov.breadcrumbContainer.Add(bucketBtn)

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

// GetContent 返回 ObjectsView 的 Fyne UI 内容
func (ov *ObjectsView) GetContent() fyne.CanvasObject {
	ov.objectList = widget.NewList(
		func() int {
			return len(ov.objects)
		},
		func() fyne.CanvasObject {
			// 列表项模板：图标 + 名称 + 大小/修改时间
			return container.NewHBox(
				widget.NewIcon(theme.FileIcon()), // 占位符图标
				widget.NewLabel("名称"),
				layout.NewSpacer(), // 填充空间
				widget.NewLabel("大小/时间"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// 更新列表项内容
			item := ov.objects[id]
			hbox := obj.(*fyne.Container)
			icon := hbox.Objects[0].(*widget.Icon)
			nameLabel := hbox.Objects[1].(*widget.Label)
			infoLabel := hbox.Objects[3].(*widget.Label) // 注意索引

			nameLabel.SetText(item.Name)
			if item.IsFolder {
				icon.SetResource(theme.FolderIcon())
				infoLabel.SetText("文件夹")
			} else {
				icon.SetResource(theme.FileIcon())
				infoLabel.SetText(fmt.Sprintf("%s | %s", formatBytes(item.Size), item.LastModified))
			}
		},
	)

	// 设置列表项点击事件 (单次点击)
	ov.objectList.OnSelected = func(id widget.ListItemID) {
		ov.selectedObjectID = id // 记录选中的 ID
		item := ov.objects[id]
		if item.IsFolder {
			// 进入子文件夹
			newPrefix := ov.currentPrefix + item.Name + "/"
			ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, newPrefix)
		} else {
			// 单击文件，目前只做选中操作，后续可扩展下载等
			log.Printf("选中文件: %s", item.Name)
		}
	}

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
				// 用户取消了选择
				return
			}
			defer reader.Close()

			filePath := reader.URI().Path()
			fileName := filepath.Base(filePath)
			s3Key := ov.currentPrefix + fileName // S3 中的完整路径

			// 读取文件内容
			data, err := ioutil.ReadAll(reader)
			if err != nil {
				dialog.ShowError(fmt.Errorf("读取文件失败: %v", err), ov.window)
				return
			}

			// 上传文件
			go func() {
				err := ov.s3Client.UploadObject(ov.currentBucket, s3Key, strings.NewReader(string(data)), int64(len(data)))
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("上传失败: %v", err), ov.window)
					} else {
						dialog.ShowInformation("成功", fmt.Sprintf("文件 %s 上传成功！", fileName), ov.window)
						ov.loadObjects() // 刷新列表
					}
				})
			}()
		}, ov.window)
		fd.Show()
	})

	// 下载按钮
	downloadButton := widget.NewButtonWithIcon("下载", theme.DownloadIcon(), func() {
		if ov.selectedObjectID == -1 || ov.selectedObjectID >= len(ov.objects) {
			dialog.ShowInformation("提示", "请先选择一个要下载的文件。", ov.window)
			return
		}
		selectedObject := ov.objects[ov.selectedObjectID]
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
				// 用户取消了保存
				return
			}
			// 移除 defer writer.Close()，手动关闭

			s3Key := ov.currentPrefix + selectedObject.Name // S3 中的完整路径

			go func() {
				body, err := ov.s3Client.DownloadObject(ov.currentBucket, s3Key)
				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("下载失败: %v", err), ov.window)
					})
					return
				}
				defer body.Close() // 确保 S3 响应体关闭

				_, err = io.Copy(writer, body)
				// 在 io.Copy 成功后手动关闭 writer
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
		fd.SetFileName(selectedObject.Name) // 预设文件名
		fd.Show()
	})

	// 删除按钮
	deleteButton := widget.NewButtonWithIcon("删除", theme.DeleteIcon(), func() {
		if ov.selectedObjectID == -1 || ov.selectedObjectID >= len(ov.objects) {
			dialog.ShowInformation("提示", "请先选择一个要删除的文件或文件夹。", ov.window)
			return
		}
		selectedObject := ov.objects[ov.selectedObjectID]

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除 \"%s\" 吗？", selectedObject.Name), func(confirmed bool) {
			if confirmed {
				s3Key := ov.currentPrefix + selectedObject.Name
				if selectedObject.IsFolder {
					s3Key += "/" // 文件夹的 key 需要以 / 结尾
				}

				go func() {
					err := ov.s3Client.DeleteObject(ov.currentBucket, s3Key)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("删除失败: %v", err), ov.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("\"%s\" 删除成功！", selectedObject.Name), ov.window)
							ov.loadObjects() // 刷新列表
						}
					})
				}()
			}
		}, ov.window)
	})

	// 文件操作按钮布局
	fileOpsButtons := container.NewHBox(uploadButton, downloadButton, deleteButton)

	// 顶部操作栏：面包屑 + 操作按钮
	topBar := container.NewBorder(nil, nil, ov.breadcrumbContainer, fileOpsButtons, nil)

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
