package ui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"

	"image/color"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/nfnt/resize"

	"s3-explorer/s3client"
)

// --- Global Cache & Custom Types ---
var (
	thumbnailCache = make(map[string]fyne.Resource)
	cacheLock      = sync.RWMutex{}
)

// thumbnailResource 实现了 fyne.Resource 接口，用于将 image.Image 包装成资源
type thumbnailResource struct {
	name string
	img  image.Image
}

func (t *thumbnailResource) Name() string {
	return t.name
}

func (t *thumbnailResource) Content() []byte {
	buf := new(bytes.Buffer)
	// 将 image.Image 编码为 PNG 字节流
	err := png.Encode(buf, t.img)
	if err != nil {
		log.Printf("无法编码缩略图: %v", err)
		return nil
	}
	return buf.Bytes()
}

// --- Custom Widgets ---

// tappableContainer 是一个可以捕获点击事件的容器
type tappableContainer struct {
	widget.BaseWidget
	content  fyne.CanvasObject
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
	selected     bool
}

// listEntryRenderer 自定义渲染器
type listEntryRenderer struct {
	entry      *listEntry
	background *canvas.Rectangle
	content    *fyne.Container
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
		entry:      e,
		background: bg,
		content:    content,
	}
}

func (e *listEntry) DoubleTapped(_ *fyne.PointEvent) {
	if e.doubleTapped != nil {
		e.doubleTapped()
	}
}

func (e *listEntry) MouseDown(m *desktop.MouseEvent) {
	e.ov.handleItemClick(e.id, m)
}

func (e *listEntry) MouseUp(_ *desktop.MouseEvent) {}

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

// minWidthEntry 是一个具有最小宽度的 Entry
type minWidthEntry struct {
	widget.Entry
	minWidth float32
}

func newMinWidthEntry(minWidth float32) *minWidthEntry {
	e := &minWidthEntry{minWidth: minWidth}
	e.ExtendBaseWidget(e)
	return e
}

func (e *minWidthEntry) MinSize() fyne.Size {
	s := e.Entry.MinSize()
	if s.Width < e.minWidth {
		s.Width = e.minWidth
	}
	return s
}

// --- Main View ---

// ObjectsView 结构体用于管理右侧的文件/文件夹列表视图
type ObjectsView struct {
	window              fyne.Window
	s3Client            *s3client.S3Client
	currentBucket       string
	currentPrefix       string
	objects             []s3client.S3Object
	objectList          *widget.List
	breadcrumbContainer *fyne.Container
	selectedObjectIDs   map[widget.ListItemID]struct{}
	lastSelectedID      widget.ListItemID
	loadingIndicator    *widget.ProgressBarInfinite
	downloadButton      *widget.Button
	deleteButton        *widget.Button
	serviceInfoButton   *widget.Button

	// 分页相关状态
	currentPage    int
	pageSize       int
	pageMarkers    []string
	nextPageMarker *string
	prevButton     *widget.Button
	nextButton     *widget.Button
	pageInfoLabel  *widget.Label
	pageSizeEntry  *minWidthEntry
}

// NewObjectsView 创建并返回一个新的 ObjectsView 实例
func NewObjectsView(w fyne.Window) *ObjectsView {
	ov := &ObjectsView{
		window:            w,
		selectedObjectIDs: make(map[widget.ListItemID]struct{}),
		lastSelectedID:    -1,
		loadingIndicator:  widget.NewProgressBarInfinite(),
		serviceInfoButton: widget.NewButton("未选择服务", func() {}),
		currentPage:       1,
		pageSize:          1000,
		pageMarkers:       []string{""},
	}
	ov.serviceInfoButton.Importance = widget.LowImportance
	ov.serviceInfoButton.Disable()
	ov.loadingIndicator.Hide()
	return ov
}

// SetServiceAlias 设置并显示当前服务的别名
func (ov *ObjectsView) SetServiceAlias(alias string) {
	if alias != "" {
		ov.serviceInfoButton.SetText(fmt.Sprintf("当前服务: %s", alias))
	} else {
		ov.serviceInfoButton.SetText("未选择服务")
	}
}

// SetBucketAndPrefix 设置当前存储桶和前缀，并加载对象列表
func (ov *ObjectsView) SetBucketAndPrefix(client *s3client.S3Client, bucket, prefix string) {
	ov.s3Client = client
	ov.currentBucket = bucket
	ov.currentPrefix = prefix

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
	ov.updatePaginationControls()

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
				if nextMarker != nil && len(ov.pageMarkers) == ov.currentPage {
					ov.pageMarkers = append(ov.pageMarkers, *nextMarker)
				}
			}
			ov.refreshObjectList()
			ov.updateButtonsState()
			ov.updatePaginationControls()
			go ov.loadThumbnails()
		})
	}()
}

// loadThumbnails 遍历当前对象列表并加载图片缩略图
func (ov *ObjectsView) loadThumbnails() {
	for i, obj := range ov.objects {
		if isPreviewableImage(obj.Name) {
			cacheLock.RLock()
			_, exists := thumbnailCache[obj.Key]
			cacheLock.RUnlock()

			if !exists {
				go ov.generateThumbnail(i, obj)
			}
		}
	}
}

// generateThumbnail 为单个图片对象生成缩略图并更新UI
func (ov *ObjectsView) generateThumbnail(index int, item s3client.S3Object) {
	body, err := ov.s3Client.DownloadObject(ov.currentBucket, item.Key)
	if err != nil {
		log.Printf("生成缩略图失败 (下载 %s): %v", item.Key, err)
		return
	}
	defer body.Close()

	data, err := ioutil.ReadAll(body)
	if err != nil {
		log.Printf("生成缩略图失败 (读取 %s): %v", item.Key, err)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		log.Printf("生成缩略图失败 (解码 %s): %v", item.Key, err)
		return
	}

	thumb := resize.Thumbnail(64, 64, img, resize.Lanczos3)
	thumbRes := &thumbnailResource{name: item.Key, img: thumb}

	cacheLock.Lock()
	thumbnailCache[item.Key] = thumbRes
	cacheLock.Unlock()

	fyne.Do(func() {
		ov.objectList.RefreshItem(index)
	})
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

// showPreviewWindow 弹出一个新窗口来预览文件
func (ov *ObjectsView) showPreviewWindow(item s3client.S3Object) {
	previewWindow := fyne.CurrentApp().NewWindow(fmt.Sprintf("预览 - %s", item.Name))
	previewWindow.SetContent(container.NewCenter(widget.NewProgressBarInfinite()))
	previewWindow.Resize(fyne.NewSize(800, 600))
	previewWindow.Show()

	go func() {
		body, err := ov.s3Client.DownloadObject(ov.currentBucket, item.Key)
		if err != nil {
			log.Printf("预览失败 (下载): %v", err)
			fyne.Do(func() { previewWindow.SetContent(container.NewCenter(widget.NewLabel("加载预览失败"))) })
			return
		}
		defer body.Close()

		data, err := ioutil.ReadAll(body)
		if err != nil {
			log.Printf("预览失败 (读取): %v", err)
			fyne.Do(func() { previewWindow.SetContent(container.NewCenter(widget.NewLabel("加载预览失败"))) })
			return
		}

		ext := strings.ToLower(filepath.Ext(item.Name))
		var previewContent fyne.CanvasObject

		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif":
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				log.Printf("预览图片失败 (解码): %v", err)
				previewContent = container.NewCenter(widget.NewLabel("无法解码图片"))
			} else {
				canvasImg := canvas.NewImageFromImage(img)
				canvasImg.FillMode = canvas.ImageFillContain
				previewContent = container.NewScroll(canvasImg)
			}
		case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".cfg", ".go", ".py", ".js", ".html", ".css":
			textEntry := widget.NewMultiLineEntry()
			textEntry.SetText(string(data))
			textEntry.Disable()
			textEntry.Wrapping = fyne.TextWrapBreak
			previewContent = container.NewScroll(textEntry)
		default:
			previewContent = container.NewCenter(widget.NewLabel(fmt.Sprintf("不支持预览 %s 类型的文件", ext)))
		}
		fyne.Do(func() { previewWindow.SetContent(previewContent) })
	}()
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
				if isPreviewableImage(item.Name) {
					cacheLock.RLock()
					thumb, exists := thumbnailCache[item.Key]
					cacheLock.RUnlock()
					if exists {
						entry.icon.SetResource(thumb)
					} else {
						entry.icon.SetResource(theme.FileImageIcon()) // 默认图片图标
					}
				} else {
					entry.icon.SetResource(getIconForFile(item.Name))
				}

				entry.infoLabel.SetText(fmt.Sprintf("%s | %s", formatBytes(item.Size), item.LastModified))
				entry.doubleTapped = func() {
					ov.showPreviewWindow(item)
				}
			}
			entry.Refresh()
		},
	)

	listContainer := newTappableContainer(ov.objectList, ov.unselectAllObjects)

	ov.breadcrumbContainer = container.NewHBox()
	ov.updateBreadcrumbs()

	createFolderButton := widget.NewButtonWithIcon("创建文件夹", theme.FolderNewIcon(), func() {
		if ov.s3Client == nil || ov.currentBucket == "" {
			dialog.ShowInformation("提示", "请先选择一个 S3 服务和存储桶。", ov.window)
			return
		}

		entry := widget.NewEntry()
		dialog.ShowForm("创建新文件夹", "创建", "取消", []*widget.FormItem{
			widget.NewFormItem("文件夹名称", entry),
		}, func(confirmed bool) {
			if confirmed {
				folderName := entry.Text
				if folderName == "" {
					dialog.ShowInformation("提示", "文件夹名称不能为空。", ov.window)
					return
				}
				s3Key := ov.currentPrefix + folderName + "/"

				go func() {
					err := ov.s3Client.CreateFolder(ov.currentBucket, s3Key)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("创建文件夹失败: %v", err), ov.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("文件夹 '%s' 创建成功！", folderName), ov.window)
							ov.loadObjects()
						}
					})
				}()
			}
		}, ov.window)
	})

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

		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, ov.window)
				return
			}
			if uri == nil {
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
						ov.loadObjects()
					})
				}()
			}
		}, ov.window)
	})
	ov.updateButtonsState()

	fileOpsButtons := container.NewHBox(createFolderButton, uploadButton, ov.downloadButton, ov.deleteButton)

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
	ov.pageSizeEntry = newMinWidthEntry(80)
	ov.pageSizeEntry.SetText(strconv.Itoa(ov.pageSize))
	ov.pageSizeEntry.OnSubmitted = func(s string) {
		ps, err := strconv.Atoi(s)
		if err != nil || ps <= 0 {
			dialog.ShowError(fmt.Errorf("无效的页面大小"), ov.window)
			ov.pageSizeEntry.SetText(strconv.Itoa(ov.pageSize))
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
	statusBar := container.NewBorder(nil, nil, ov.serviceInfoButton, pagingControls, nil)

	// --- 主内容区 ---
	return container.NewBorder(topBar, statusBar, nil, nil, listContainer)
}

// startDownloadProcess 启动下载流程
func (ov *ObjectsView) startDownloadProcess(localBasePath string) {
	progressDialog := dialog.NewProgressInfinite("正在下载", "请稍候...", ov.window)
	progressDialog.Show()
	defer progressDialog.Hide()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failedDownloads []string

	objectsToDownload := make(chan s3client.S3Object, len(ov.selectedObjectIDs))

	numWorkers := 10
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for obj := range objectsToDownload {
				ov.processDownloadItem(obj, localBasePath, &failedDownloads, &mu)
			}
		}()
	}

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
		ov.downloadFolder(obj, localBasePath, failedDownloads, mu)
	} else {
		ov.downloadFile(obj, localBasePath, failedDownloads, mu)
	}
}

// downloadFile 下载单个文件
func (ov *ObjectsView) downloadFile(obj s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex) {
	localPath := filepath.Join(localBasePath, obj.Name)

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		log.Printf("创建本地目录失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}

	localFile, err := os.Create(localPath)
	if err != nil {
		log.Printf("创建本地文件失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}
	defer localFile.Close()

	body, err := ov.s3Client.DownloadObject(ov.currentBucket, obj.Key)
	if err != nil {
		log.Printf("从 S3 下载失败: %v", err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, obj.Name)
		mu.Unlock()
		return
	}
	defer body.Close()

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
	objectsToDownload, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, folder.Key)
	if err != nil {
		log.Printf("列出文件夹 '%s' 内容失败: %v", folder.Name, err)
		mu.Lock()
		*failedDownloads = append(*failedDownloads, folder.Name)
		mu.Unlock()
		return
	}

	var wg sync.WaitGroup
	for _, obj := range objectsToDownload {
		wg.Add(1)
		go func(fileToDownload s3client.S3Object) {
			defer wg.Done()
			relativePath := strings.TrimPrefix(fileToDownload.Key, folder.Key)
			localPath := filepath.Join(localBasePath, folder.Name, relativePath)
			ov.downloadFile(s3client.S3Object{Name: filepath.Base(localPath), Key: fileToDownload.Key}, filepath.Dir(localPath), failedDownloads, mu)
		}(obj)
	}
	wg.Wait()
}

// getIconForFile 根据文件名返回对应的图标
func getIconForFile(name string) fyne.Resource {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".svg", ".webp":
		return theme.FileImageIcon()
	case ".mp3", ".wav", ".ogg", ".flac":
		return theme.FileAudioIcon()
	case ".mp4", ".avi", ".mov", ".mkv", ".webm":
		return theme.FileVideoIcon()
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2":
		return theme.FileIcon()
	case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".cfg":
		return theme.FileTextIcon()
	default:
		return theme.FileIcon()
	}
}

func isPreviewableImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	default:
		return false
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
