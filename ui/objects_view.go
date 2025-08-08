package ui

import (
	"fmt"
	"image/color"
	"os"
	"strconv"

	"fyne.io/fyne/v2/canvas"
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

// tappableContainer 是一个可以捕获点击事件的容器
type tappableContainer struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTapped func()
}

func (c *tappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *tappableContainer) Tapped(_ *fyne.PointEvent) {
	if c.onTapped != nil {
		c.onTapped()
	}
}

func newTappableContainer(content fyne.CanvasObject, onTapped func()) *tappableContainer {
	c := &tappableContainer{
		content:  content,
		onTapped: onTapped,
	}
	c.ExtendBaseWidget(c)
	return c
}

// listEntry 是一个自定义的列表项组件，用于处理双击和带修饰键的点击
type listEntry struct {
	widget.BaseWidget
	icon      *widget.Icon
	nameLabel *widget.Label
	infoLabel *widget.Label

	id widget.ListItemID
	ov *ObjectsView // 指向父视图的引用

	doubleTapped func()
	selected bool
}

// listEntryRenderer 自定义渲染器
type listEntryRenderer struct {
	entry       *listEntry
	background  *canvas.Rectangle
	content     *fyne.Container
}

func (r *listEntryRenderer) Destroy() {}

func (r *listEntryRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.content.Resize(size)
}

func (r *listEntryRenderer) MinSize() fyne.Size {
	return r.content.MinSize()
}

func (r *listEntryRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.background, r.content}
}

// Refresh 根据选中状态更新背景色
func (r *listEntryRenderer) Refresh() {
	if r.entry.selected {
		r.background.FillColor = theme.SelectionColor()
	} else {
		r.background.FillColor = color.Transparent
	}
	r.background.Refresh()
	canvas.Refresh(r.entry)
}

func (e *listEntry) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	content := container.NewHBox(
		e.icon,
		e.nameLabel,
		layout.NewSpacer(),
		e.infoLabel,
	)
	return &listEntryRenderer{
		entry:       e,
		background:  bg,
		content:     content,
	}
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
	serviceInfoLabel    *widget.Label // 用于显示当前服务信息的标签

	// 分页相关状态
	currentPage       int
	pageSize          int
	pageMarkers       []string // 存储每一页的起始 marker
	nextPageMarker    *string
	prevButton        *widget.Button
	nextButton        *widget.Button
	pageInfoLabel     *widget.Label
	pageSizeEntry     *widget.Entry
}

// NewObjectsView 创建并返回一个新的 ObjectsView 实例
func NewObjectsView(w fyne.Window) *ObjectsView {
	ov := &ObjectsView{
		window:            w,
		selectedObjectIDs: make(map[widget.ListItemID]struct{}),
		lastSelectedID:    -1,
		loadingIndicator:  widget.NewProgressBarInfinite(),
		serviceInfoLabel:  widget.NewLabel("未选择服务"),
		currentPage:       1,
		pageSize:          100, // 默认每页 100 条
		pageMarkers:       []string{""}, // 第1页的 marker 是空字符串
	}
	ov.serviceInfoLabel.Alignment = fyne.TextAlignCenter
	ov.loadingIndicator.Hide()
	return ov
}

// SetServiceAlias 设置并显示当前服务的别名
func (ov *ObjectsView) SetServiceAlias(alias string) {
	if alias != "" {
		ov.serviceInfoLabel.SetText(fmt.Sprintf("当前服务: %s", alias))
	} else {
		ov.serviceInfoLabel.SetText("未选择服务")
	}
}

// SetBucketAndPrefix 设置当前存储桶和前缀，并加载对象列表
func (ov *ObjectsView) SetBucketAndPrefix(client *s3client.S3Client, bucket, prefix string) {
	ov.s3Client = client
	ov.currentBucket = bucket
	ov.currentPrefix = prefix

	// 重置分页和选择状态
	ov.resetPagingAndSelection()
	ov.loadObjects()
	ov.updateBreadcrumbs()
}

func (ov *ObjectsView) resetPagingAndSelection() {
	ov.currentPage = 1
	ov.pageMarkers = []string{""}
	ov.nextPageMarker = nil
	ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
	ov.lastSelectedID = -1
	ov.updateButtonsState()
	ov.updatePaginationControls()
}

// loadObjects 加载指定存储桶和前缀下的对象列表
func (ov *ObjectsView) loadObjects() {
	if ov.s3Client == nil || ov.currentBucket == "" {
		ov.objects = []s3client.S3Object{}
		ov.refreshObjectList()
		ov.updateButtonsState()
		ov.updatePaginationControls()
		return
	}

	ov.loadingIndicator.Show()
	ov.updatePaginationControls() // 禁用按钮

	go func() {
		marker := ov.pageMarkers[ov.currentPage-1]
		objects, nextMarker, err := ov.s3Client.ListObjects(ov.currentBucket, ov.currentPrefix, marker, int32(ov.pageSize))

		fyne.Do(func() {
			ov.loadingIndicator.Hide()
			if err != nil {
				log.Printf("列出对象失败: %v", err)
				dialog.ShowError(fmt.Errorf("列出对象失败: %v", err), ov.window)
				ov.objects = []s3client.S3Object{}
			} else {
				ov.objects = objects
				ov.nextPageMarker = nextMarker
				// 如果有下一页，并且 markers 数组中还没有记录，则添加
				if nextMarker != nil && len(ov.pageMarkers) == ov.currentPage {
					ov.pageMarkers = append(ov.pageMarkers, *nextMarker)
				}
			}
			ov.refreshObjectList()
			ov.updateButtonsState()
			ov.updatePaginationControls()
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

	ov.breadcrumbContainer.RemoveAll()

	if ov.currentBucket != "" {
		bucketBtn := widget.NewButton(ov.currentBucket, func() {
			ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, "")
		})
		ov.breadcrumbContainer.Add(bucketBtn)
	}

	if ov.currentPrefix != "" {
		pathSegments := strings.Split(strings.TrimSuffix(ov.currentPrefix, "/"), "/")
		currentPath := ""
		for _, segment := range pathSegments {
			if segment == "" {
				continue
			}
			currentPath += segment + "/"
			pathForClosure := currentPath
			segmentBtn := widget.NewButton(segment, func() {
				ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, pathForClosure)
			})
			ov.breadcrumbContainer.Add(widget.NewLabel(">"))
			ov.breadcrumbContainer.Add(segmentBtn)
		}
	}
	ov.breadcrumbContainer.Refresh()
}

// handleItemClick 处理列表项的点击事件，包含多选逻辑
func (ov *ObjectsView) handleItemClick(id widget.ListItemID, m *desktop.MouseEvent) {
	if m.Button == desktop.MouseButtonSecondary {
		return
	}

	ctrl := m.Modifier&desktop.ControlModifier != 0 || m.Modifier&desktop.SuperModifier != 0
	shift := m.Modifier&desktop.ShiftModifier != 0

	if !ctrl && !shift {
		if _, selected := ov.selectedObjectIDs[id]; selected && len(ov.selectedObjectIDs) == 1 {
			ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
			ov.lastSelectedID = -1
		} else {
			ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
			ov.selectedObjectIDs[id] = struct{}{}
			ov.lastSelectedID = id
		}
	} else if ctrl {
		if _, selected := ov.selectedObjectIDs[id]; selected {
			delete(ov.selectedObjectIDs, id)
		} else {
			ov.selectedObjectIDs[id] = struct{}{}
		}
		ov.lastSelectedID = id
	} else if shift {
		if ov.lastSelectedID == -1 {
			ov.selectedObjectIDs[id] = struct{}{}
			ov.lastSelectedID = id
		} else {
			start, end := ov.lastSelectedID, id
			if start > end {
				start, end = end, start
			}
			for i := start; i <= end; i++ {
				ov.selectedObjectIDs[i] = struct{}{}
			}
		}
	}
	ov.objectList.Refresh()
	ov.updateButtonsState()
}

// unselectAllObjects 取消所有对象的选择
func (ov *ObjectsView) unselectAllObjects() {
	if len(ov.selectedObjectIDs) > 0 {
		ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
		ov.lastSelectedID = -1
		ov.objectList.Refresh()
		ov.updateButtonsState()
	}
}

// updateButtonsState 根据当前选择状态更新按钮的可用性
func (ov *ObjectsView) updateButtonsState() {
	if ov.downloadButton == nil || ov.deleteButton == nil {
		return
	}

	numSelected := len(ov.selectedObjectIDs)

	if numSelected > 0 {
		ov.deleteButton.Enable()
		ov.downloadButton.Enable()
	} else {
		ov.deleteButton.Disable()
		ov.downloadButton.Disable()
	}
}

func (ov *ObjectsView) updatePaginationControls() {
	if ov.pageInfoLabel == nil || ov.prevButton == nil || ov.nextButton == nil {
		return
	}

	ov.pageInfoLabel.SetText(fmt.Sprintf("第 %d 页", ov.currentPage))

	if ov.currentPage > 1 {
		ov.prevButton.Enable()
	} else {
		ov.prevButton.Disable()
	}

	if ov.nextPageMarker != nil {
		ov.nextButton.Enable()
	} else {
		ov.nextButton.Disable()
	}

	if ov.loadingIndicator.Visible() {
		ov.prevButton.Disable()
		ov.nextButton.Disable()
	}
}

// GetContent 返回 ObjectsView 的 Fyne UI 内容
func (ov *ObjectsView) GetContent() fyne.CanvasObject {
	ov.objectList = widget.NewList(
		func() int {
			return len(ov.objects)
		},
		func() fyne.CanvasObject {
			return newListEntry(ov)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			item := ov.objects[id]
			entry := obj.(*listEntry)
			entry.id = id
			entry.nameLabel.SetText(item.Name)
			_, entry.selected = ov.selectedObjectIDs[id]

			if item.IsFolder {
				entry.icon.SetResource(theme.FolderIcon())
				entry.infoLabel.SetText("文件夹")
				entry.doubleTapped = func() {
					ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, item.Key)
				}
			} else {
				entry.icon.SetResource(getIconForFile(item.Name))
				entry.infoLabel.SetText(fmt.Sprintf("%s | %s", formatBytes(item.Size), item.LastModified))
				entry.doubleTapped = nil
			}
			entry.Refresh()
		},
	)

	listContainer := newTappableContainer(ov.objectList, ov.unselectAllObjects)

	ov.breadcrumbContainer = container.NewHBox()
	ov.updateBreadcrumbs()

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
	ov.downloadButton = widget.NewButtonWithIcon("下载", theme.DownloadIcon(), func() {
		if len(ov.selectedObjectIDs) == 0 {
			dialog.ShowInformation("提示", "请至少选择一个要下载的项目。", ov.window)
			return
		}

		// 弹出文件夹选择对话框
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, ov.window)
				return
			}
			if uri == nil {
				// 用户取消了选择
				return
			}
			go ov.startDownloadProcess(uri.Path())
		}, ov.window)
	})
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
							s3Key := selectedObject.Key
							if selectedObject.IsFolder {
								// 为了删除文件夹，我们需要删除其下的所有对象
								// 这是一个复杂操作，S3 本身没有原子性的文件夹删除
								// 简单起见，我们目前只删除代表文件夹的那个对象（如果存在）
								// 注意：这通常不会删除文件夹里的内容
								if !strings.HasSuffix(s3Key, "/") {
									s3Key += "/"
								}
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
	ov.updateButtonsState()

	fileOpsButtons := container.NewHBox(uploadButton, ov.downloadButton, ov.deleteButton)

	topBar := container.NewBorder(nil, nil, ov.breadcrumbContainer, fileOpsButtons, nil)

	// --- 分页控件 ---
	ov.prevButton = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if ov.currentPage > 1 {
			ov.currentPage--
			ov.loadObjects()
		}
	})
	ov.nextButton = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		if ov.nextPageMarker != nil {
			ov.currentPage++
			ov.loadObjects()
		}
	})
	ov.pageInfoLabel = widget.NewLabel("")
	ov.pageSizeEntry = widget.NewEntry()
	ov.pageSizeEntry.SetText(strconv.Itoa(ov.pageSize))
	ov.pageSizeEntry.OnSubmitted = func(s string) {
		ps, err := strconv.Atoi(s)
		if err != nil || ps <= 0 {
			dialog.ShowError(fmt.Errorf("无效的页面大小"), ov.window)
			ov.pageSizeEntry.SetText(strconv.Itoa(ov.pageSize)) // 恢复原值
			return
		}
		ov.pageSize = ps
		ov.resetPagingAndSelection()
		ov.loadObjects()
	}

	pagingControls := container.NewHBox(
		layout.NewSpacer(),
		widget.NewLabel("每页显示:"),
		ov.pageSizeEntry,
		ov.prevButton,
		ov.pageInfoLabel,
		ov.nextButton,
	)

	ov.updatePaginationControls()

	// --- 底部状态栏 ---
	// 创建一个灰色的标签用于显示服务信息
	serviceInfoLabelContainer := container.NewCenter(ov.serviceInfoLabel)
	ov.serviceInfoLabel.TextStyle.Italic = true

	statusBar := container.NewBorder(nil, nil, nil, pagingControls, serviceInfoLabelContainer)

	return container.NewBorder(topBar, statusBar, nil, nil, listContainer)
}

// startDownloadProcess 启动下载流程
func (ov *ObjectsView) startDownloadProcess(localBasePath string) {
	// 显示一个加载对话框
	progressDialog := dialog.NewProgressInfinite("正在下载", "请稍候...", ov.window)
	progressDialog.Show()
	defer progressDialog.Hide()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failedDownloads []string

	objectsToDownload := make(chan s3client.S3Object, len(ov.selectedObjectIDs))

	// 启动固定数量的 worker goroutine
	numWorkers := 10 // 可以根据需要调整并发数
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for obj := range objectsToDownload {
				ov.processDownloadItem(obj, localBasePath, &failedDownloads, &mu)
			}
		}()
	}

	// 遍历选中的项目并添加到下载队列
	for id := range ov.selectedObjectIDs {
		if id < len(ov.objects) {
			objectsToDownload <- ov.objects[id]
		}
	}
	close(objectsToDownload)

	wg.Wait()

	fyne.Do(func() {
		if len(failedDownloads) > 0 {
			dialog.ShowError(fmt.Errorf("部分项目下载失败: %s", strings.Join(failedDownloads, ", ")), ov.window)
		} else {
			dialog.ShowInformation("成功", "所有项目下载完成。", ov.window)
		}
	})
}

// processDownloadItem 处理单个项目（文件或文件夹）的下载
func (ov *ObjectsView) processDownloadItem(obj s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex) {
	if obj.IsFolder {
		// 递归下载文件夹
		ov.downloadFolder(obj, localBasePath, failedDownloads, mu)
	} else {
		// 下载单个文件
		ov.downloadFile(obj, localBasePath, failedDownloads, mu)
	}
}

// downloadFile 下载单个文件
func (ov *ObjectsView) downloadFile(obj s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex) {
	// 修正：当直接下载文件时，它的相对路径就是它的名字
	localPath := filepath.Join(localBasePath, obj.Name)

	// 确保本地目录存在
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		log.Printf("创建本地目录失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}

	// 创建本地文件
	localFile, err := os.Create(localPath)
	if err != nil {
		log.Printf("创建本地文件失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}
	defer localFile.Close()

	// 从 S3 下载
	body, err := ov.s3Client.DownloadObject(ov.currentBucket, obj.Key)
	if err != nil {
		log.Printf("从 S3 下载失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}
	defer body.Close()

	// 写入文件
	_, err = io.Copy(localFile, body)
	if err != nil {
		log.Printf("写入本地文件失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
	}
}

// downloadFolder 递归下载文件夹
func (ov *ObjectsView) downloadFolder(folder s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex) {
	// 获取文件夹下的所有文件
	objectsToDownload, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, folder.Key)
	if err != nil {
		log.Printf("列出文件夹 '%s' 内容失败: %v", folder.Name, err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, folder.Name)
		mu.Unlock()
		return
	}

	// 为每个文件启动一个 goroutine 进行下载
	var wg sync.WaitGroup
	for _, obj := range objectsToDownload {
		wg.Add(1)
		go func(fileToDownload s3client.S3Object) {
			defer wg.Done()
			// 计算相对路径，以保持目录结构
			// folder.Key 是类似 "photos/2024/"
			// fileToDownload.Key 是类似 "photos/2024/image1.jpg"
			// 我们需要得到 "image1.jpg"
			relativePath := strings.TrimPrefix(fileToDownload.Key, folder.Key)

			// 构建完整的本地保存路径
			// localBasePath 是用户选择的目录，例如 "/Users/me/Downloads"
			// folder.Name 是 "2024"
			// relativePath 是 "image1.jpg"
			// 最终路径是 "/Users/me/Downloads/2024/image1.jpg"
			localPath := filepath.Join(localBasePath, folder.Name, relativePath)

			// 下载这个文件，它的逻辑名称是它的相对路径
			ov.downloadFile(s3client.S3Object{Name: filepath.Base(localPath), Key: fileToDownload.Key}, filepath.Dir(localPath), failedDownloads, mu)
		}(obj)
	}
	wg.Wait()
}

// getIconForFile 根据文件名返回对应的图标
func getIconForFile(name string) fyne.Resource {
	// 注意：Fyne v2.6.2 的内置主题图标比较有限。
	// 这里我们使用该版本支持的图标，对于不支持的类型，统一回退到通用文件图标。
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".svg", ".webp":
		// v2.6.2 中没有 FileImageIcon，使用通用图标
		return theme.FileIcon()
	case ".mp3", ".wav", ".ogg", ".flac":
		// v2.6.2 中没有 FileAudioIcon，使用通用图标
		return theme.FileIcon()
	case ".mp4", ".avi", ".mov", ".mkv", ".webm":
		// v2.6.2 中没有 FileVideoIcon，使用通用图标
		return theme.FileIcon()
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2":
		// v2.6.2 中没有 FileArchiveIcon，使用通用图标
		return theme.FileIcon()
	case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".cfg":
		return theme.FileTextIcon()
	default:
		return theme.FileIcon()
	}
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