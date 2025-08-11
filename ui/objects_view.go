package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// --- 全局缓存与自定义类型 ---
var (
	thumbnailCache = make(map[string]fyne.Resource)
	cacheLock      = sync.RWMutex{}
)

const (
	listViewMode = "list"
	gridViewMode = "grid"
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

// --- 自定义组件 ---

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

// --- Grid Entry Widget ---

type gridEntry struct {
	widget.BaseWidget
	icon      *widget.Icon // 使用 widget.Icon 以便资源更新后能自动刷新
	nameLabel *widget.Label

	id widget.ListItemID
	ov *ObjectsView

	doubleTapped func()
	selected     bool
}

type gridEntryRenderer struct {
	entry      *gridEntry
	background *canvas.Rectangle
	content    *fyne.Container
}

func (r *gridEntryRenderer) Destroy() {}

func (r *gridEntryRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.content.Resize(size)
}

func (r *gridEntryRenderer) MinSize() fyne.Size {
	return r.content.MinSize()
}

func (r *gridEntryRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.background, r.content}
}

func (r *gridEntryRenderer) Refresh() {
	if r.entry.selected {
		r.background.FillColor = theme.SelectionColor()
	} else {
		r.background.FillColor = color.Transparent
	}
	r.background.Refresh()
	canvas.Refresh(r.entry)
}

func (e *gridEntry) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	// 使用 Border 布局，图标在上，标签在下
	content := container.NewBorder(nil, e.nameLabel, nil, nil, e.icon)
	return &gridEntryRenderer{
		entry:      e,
		background: bg,
		content:    content,
	}
}

func (e *gridEntry) DoubleTapped(_ *fyne.PointEvent) {
	if e.doubleTapped != nil {
		e.doubleTapped()
	}
}

func (e *gridEntry) MouseDown(m *desktop.MouseEvent) {
	e.ov.handleItemClick(e.id, m)
}

func (e *gridEntry) MouseUp(_ *desktop.MouseEvent) {}

func newGridEntry(ov *ObjectsView) *gridEntry {
	icon := widget.NewIcon(theme.FileIcon())
	nameLabel := widget.NewLabel("Filename")
	nameLabel.Wrapping = fyne.TextWrapWord
	nameLabel.Alignment = fyne.TextAlignCenter

	entry := &gridEntry{
		icon:      icon,
		nameLabel: nameLabel,
		ov:        ov,
	}
	entry.ExtendBaseWidget(entry)
	return entry
}

// --- 主视图 ---

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

	// 视图切换
	viewMode            string
	viewSwitchButton    *widget.Button
	mainContent         *fyne.Container
	currentServiceAlias string

	// OnViewModeChanged 是一个回调函数，当视图模式改变时触发
	OnViewModeChanged func(alias, newMode string)
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
		viewMode:          listViewMode, // 默认是列表视图
	}
	ov.serviceInfoButton.Importance = widget.LowImportance
	ov.serviceInfoButton.Disable()
	ov.loadingIndicator.Hide()

	ov.window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		ov.handleDrop(uris)
	})

	return ov
}

// SetViewMode 设置当前对象视图的模式（列表或网格）
func (ov *ObjectsView) SetViewMode(mode string) {
	if ov.viewSwitchButton == nil {
		return
	}
	if mode == gridViewMode {
		ov.viewMode = gridViewMode
		ov.viewSwitchButton.SetIcon(theme.ListIcon())
	} else {
		ov.viewMode = listViewMode
		ov.viewSwitchButton.SetIcon(theme.GridIcon())
	}
	ov.refreshObjectView()
}

// SetServiceAlias 设置并显示当前服务的别名
func (ov *ObjectsView) SetServiceAlias(alias string) {
	ov.currentServiceAlias = alias
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
		ov.refreshObjectView()
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
			ov.refreshObjectView()
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

	thumb := resize.Thumbnail(80, 80, img, resize.Lanczos3)
	thumbRes := &thumbnailResource{name: item.Key, img: thumb}

	cacheLock.Lock()
	thumbnailCache[item.Key] = thumbRes
	cacheLock.Unlock()

	fyne.Do(func() {
		if ov.viewMode == listViewMode {
			if ov.objectList != nil {
				ov.objectList.RefreshItem(index)
			}
		} else {
			if ov.mainContent != nil && len(ov.mainContent.Objects) > 0 {
				if scroll, ok := ov.mainContent.Objects[0].(*container.Scroll); ok {
					if grid, ok := scroll.Content.(*fyne.Container); ok {
						if index < len(grid.Objects) {
							if entry, ok := grid.Objects[index].(*gridEntry); ok {
								entry.icon.SetResource(thumbRes)
							}
						}
					}
				}
			}
		}
	})
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
	ov.refreshSelection()
	ov.updateButtonsState()
}

// unselectAllObjects 取消所有对象的选择
func (ov *ObjectsView) unselectAllObjects() {
	if len(ov.selectedObjectIDs) > 0 {
		ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
		ov.lastSelectedID = -1
		ov.refreshSelection()
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

// showPreviewWindow 弹出一个新窗口来预览文件，或使用系统默认应用打开
func (ov *ObjectsView) showPreviewWindow(item s3client.S3Object) {
	ext := strings.ToLower(filepath.Ext(item.Name))

	// 定义可直接在 Fyne 中预览的类型
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif":
		ov.showInAppPreview(item, "image")
	case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".cfg", ".go", ".py", ".js", ".html", ".css":
		ov.showInAppPreview(item, "text")
	default:
		// 对于其他类型，下载到临时文件并用系统默认应用打开
		ov.openWithDefaultApp(item)
	}
}

// showInAppPreview 在应用内的新窗口中显示预览
func (ov *ObjectsView) showInAppPreview(item s3client.S3Object, previewType string) {
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

		var previewContent fyne.CanvasObject
		if previewType == "image" {
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				log.Printf("预览图片失败 (解码): %v", err)
				previewContent = container.NewCenter(widget.NewLabel("无法解码图片"))
			} else {
				canvasImg := canvas.NewImageFromImage(img)
				canvasImg.FillMode = canvas.ImageFillContain
				previewContent = container.NewScroll(canvasImg)
			}
		} else {
			ext := strings.ToLower(filepath.Ext(item.Name))
			originalText := string(data)

			if ext == ".md" {
				// 左侧：原始 Markdown 文本
				rawText := widget.NewMultiLineEntry()
				rawText.SetText(originalText)
				rawText.Wrapping = fyne.TextWrapBreak
				rawText.OnChanged = func(s string) { // 实现只读
					if s != originalText {
						rawText.SetText(originalText)
					}
				}

				// 右侧：渲染后的 Markdown
				renderedText := widget.NewRichTextFromMarkdown(originalText)
				renderedText.Wrapping = fyne.TextWrapBreak

				split := container.NewHSplit(
					container.NewScroll(rawText),
					container.NewScroll(renderedText),
				)
				split.Offset = 0.5
				previewContent = split
			} else {
				// 其他文本文件：使用只读的 MultiLineEntry
				textEntry := widget.NewMultiLineEntry()
				textEntry.SetText(originalText)
				textEntry.Wrapping = fyne.TextWrapBreak
				textEntry.OnChanged = func(s string) {
					if s != originalText {
						textEntry.SetText(originalText)
					}
				}
				previewContent = container.NewScroll(textEntry)
			}
		}
		fyne.Do(func() { previewWindow.SetContent(previewContent) })
	}()
}

// openWithDefaultApp 下载文件到临时目录并用系统默认应用打开
func (ov *ObjectsView) openWithDefaultApp(item s3client.S3Object) {
	loadingDialog := dialog.NewProgressInfinite("正在准备预览", "正在下载文件...", ov.window)
	loadingDialog.Show()

	go func() {
		defer loadingDialog.Hide()

		body, err := ov.s3Client.DownloadObject(ov.currentBucket, item.Key)
		if err != nil {
			log.Printf("打开文件失败 (下载): %v", err)
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("下载文件失败: %v", err), ov.window) })
			return
		}
		defer body.Close()

		// 修正：创建带正确扩展名的临时文件
		tempFile, err := ioutil.TempFile("", fmt.Sprintf("s3-explorer-*%s", filepath.Ext(item.Name)))
		if err != nil {
			log.Printf("创建临时文件失败: %v", err)
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("创建临时文件失败: %v", err), ov.window) })
			return
		}
		defer tempFile.Close()

		_, err = io.Copy(tempFile, body)
		if err != nil {
			log.Printf("写入临时文件失败: %v", err)
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("写入临时文件失败: %v", err), ov.window) })
			return
		}

		// 获取临时文件路径并用系统命令打开
		tempFilePath := tempFile.Name()
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("cmd", "/C", "start", tempFilePath)
		case "darwin":
			cmd = exec.Command("open", tempFilePath)
		default: // linux, freebsd, openbsd, netbsd
			cmd = exec.Command("xdg-open", tempFilePath)
		}

		if err := cmd.Start(); err != nil {
			log.Printf("打开外部应用失败: %v", err)
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("无法使用默认应用打开文件: %v", err), ov.window) })
		}
	}()
}

// handleDrop handles dropped files and folders
func (ov *ObjectsView) handleDrop(uris []fyne.URI) {
	if ov.s3Client == nil || ov.currentBucket == "" {
		dialog.ShowInformation("提示", "请先选择一个 S3 服务和存储桶才能上传。", ov.window)
		return
	}
	if len(uris) == 0 {
		return
	}

	log.Printf("接收到 %d 个拖放项目", len(uris))

	for _, uri := range uris {
		if uri.Scheme() != "file" {
			log.Printf("跳过非文件拖放项目: %s", uri)
			continue
		}
		path := uri.Path()
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("无法获取拖放项目信息 %s: %v", path, err)
			dialog.ShowError(fmt.Errorf("无法读取项目 '%s': %v", filepath.Base(path), err), ov.window)
			continue
		}

		if info.IsDir() {
			go ov.startUploadFolderProcess(path)
		} else {
			go ov.uploadSingleFile(path)
		}
	}
}

// uploadSingleFile handles the upload of a single file from a local path
func (ov *ObjectsView) uploadSingleFile(localPath string) {
	fileName := filepath.Base(localPath)
	s3Key := ov.currentPrefix + fileName

	file, err := os.Open(localPath)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("无法打开文件 '%s': %v", fileName, err), ov.window)
		})
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("无法读取文件信息 '%s': %v", fileName, err), ov.window)
		})
		return
	}
	fileSize := info.Size()

	err = ov.s3Client.UploadObject(ov.currentBucket, s3Key, file, fileSize)

	fyne.Do(func() {
		if err != nil {
			dialog.ShowError(fmt.Errorf("上传文件 '%s' 失败: %v", fileName, err), ov.window)
		} else {
			dialog.ShowInformation("成功", fmt.Sprintf("文件 '%s' 上传成功！", fileName), ov.window)
			ov.loadObjects()
		}
	})
}

// refreshObjectView is called when the data changes (loadObjects) or view mode switches.
func (ov *ObjectsView) refreshObjectView() {
	if ov.mainContent == nil {
		return
	}
	ov.unselectAllObjects()
	if ov.viewMode == gridViewMode {
		ov.mainContent.Objects = []fyne.CanvasObject{ov.createGridView()}
	} else {
		ov.mainContent.Objects = []fyne.CanvasObject{ov.createListView()}
	}
	ov.mainContent.Refresh()
}

// refreshSelection is called when an item is selected/deselected.
func (ov *ObjectsView) refreshSelection() {
	if ov.viewMode == gridViewMode {
		if ov.mainContent != nil && len(ov.mainContent.Objects) > 0 {
			if scroll, ok := ov.mainContent.Objects[0].(*container.Scroll); ok {
				if grid, ok := scroll.Content.(*fyne.Container); ok {
					for id, obj := range grid.Objects {
						if entry, ok := obj.(*gridEntry); ok {
							_, entry.selected = ov.selectedObjectIDs[id]
							entry.Refresh()
						}
					}
				}
			}
		}
	} else {
		if ov.objectList != nil {
			ov.objectList.Refresh()
		}
	}
}

func (ov *ObjectsView) createListView() fyne.CanvasObject {
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
	return newTappableContainer(ov.objectList, ov.unselectAllObjects)
}

func (ov *ObjectsView) createGridView() fyne.CanvasObject {
	var items []fyne.CanvasObject
	for i := 0; i < len(ov.objects); i++ {
		item := ov.objects[i]
		entry := newGridEntry(ov)
		entry.id = i
		entry.nameLabel.SetText(item.Name)
		_, entry.selected = ov.selectedObjectIDs[i]

		if item.IsFolder {
			entry.icon.SetResource(theme.FolderIcon())
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
					entry.icon.SetResource(theme.FileImageIcon())
				}
			} else {
				entry.icon.SetResource(getIconForFile(item.Name))
			}
			entry.doubleTapped = func() {
				ov.showPreviewWindow(item)
			}
		}
		items = append(items, entry)
	}

	grid := container.NewGridWrap(fyne.NewSize(120, 120), items...)
	return container.NewScroll(grid)
}

// GetContent 返回 ObjectsView 的 Fyne UI 内容
func (ov *ObjectsView) GetContent() fyne.CanvasObject {
	ov.breadcrumbContainer = container.NewHBox()
	ov.updateBreadcrumbs()

	createFolderButton := widget.NewButtonWithIcon("", theme.FolderNewIcon(), func() {
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

	uploadButton := widget.NewButtonWithIcon("", theme.UploadIcon(), func() {
		if ov.s3Client == nil || ov.currentBucket == "" {
			dialog.ShowInformation("提示", "请先选择一个 S3 服务和存储桶。", ov.window)
			return
		}

		var d dialog.Dialog

		fileUploadFunc := func() {
			d.Hide()
			fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
				if err != nil {
					dialog.ShowError(err, ov.window)
					return
				}
				if reader == nil {
					return
				}
				defer reader.Close()
				go ov.uploadSingleFile(reader.URI().Path())
			}, ov.window)
			fd.Show()
		}

		folderUploadFunc := func() {
			d.Hide()
			dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
				if err != nil {
					dialog.ShowError(err, ov.window)
					return
				}
				if uri == nil {
					return
				}
				go ov.startUploadFolderProcess(uri.Path())
			}, ov.window)
		}

		fileBtn := widget.NewButtonWithIcon("上传文件", theme.FileIcon(), fileUploadFunc)
		folderBtn := widget.NewButtonWithIcon("上传文件夹", theme.FolderIcon(), folderUploadFunc)

		content := container.NewVBox(
			widget.NewLabel("请选择要上传的类型："),
			fileBtn,
			folderBtn,
		)

		d = dialog.NewCustom("上传", "取消", content, ov.window)
		d.Show()
	})

	ov.downloadButton = widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
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
	ov.deleteButton = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
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

							var err error
							if selectedObject.IsFolder {
								s3Prefix := selectedObject.Key
								if !strings.HasSuffix(s3Prefix, "/") {
									s3Prefix += "/"
								}
								err = ov.deleteFolderAndContents(ov.currentBucket, s3Prefix)
							} else {
								err = ov.s3Client.DeleteObject(ov.currentBucket, selectedObject.Key)
							}

							if err != nil {
								mu.Lock()
								failedDeletions = append(failedDeletions, selectedObject.Name)
								mu.Unlock()
								log.Printf("删除项目 '%s' 失败: %v", selectedObject.Name, err)
							}
						}(ov.objects[id])
					}
					wg.Wait()

					fyne.Do(func() {
						if len(failedDeletions) > 0 {
							dialog.ShowError(fmt.Errorf("部分项目删除失败: %s", strings.Join(failedDeletions, ", ")), ov.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("%d 个项目已成功删除。", selectedCount), ov.window)
						}
						ov.resetPagingAndSelection()
						ov.loadObjects()
					})
				}()
			}
		}, ov.window)
	})
	ov.updateButtonsState()

	ov.viewSwitchButton = widget.NewButtonWithIcon("", theme.GridIcon(), func() {
		if ov.viewMode == listViewMode {
			ov.viewMode = gridViewMode
			ov.viewSwitchButton.SetIcon(theme.ListIcon())
		} else {
			ov.viewMode = listViewMode
			ov.viewSwitchButton.SetIcon(theme.GridIcon())
		}

		// 通过回调通知父级保存视图偏好
		if ov.OnViewModeChanged != nil && ov.currentServiceAlias != "" {
			go ov.OnViewModeChanged(ov.currentServiceAlias, ov.viewMode)
		}

		ov.refreshObjectView()
	})

	fileOpsButtons := container.NewHBox(createFolderButton, uploadButton, ov.downloadButton, ov.deleteButton, ov.viewSwitchButton)

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
	ov.mainContent = container.NewMax()
	ov.refreshObjectView() // Initial view

	return container.NewBorder(topBar, statusBar, nil, nil, container.NewVBox(widget.NewSeparator()), ov.mainContent)
}

// startUploadFolderProcess 启动文件夹上传流程
func (ov *ObjectsView) startUploadFolderProcess(localPath string) {
	progressDialog := dialog.NewProgressInfinite("正在上传文件夹", "正在扫描...", ov.window)
	progressDialog.Show()
	defer progressDialog.Hide()

	baseFolderName := filepath.Base(localPath)

	var filesToUpload []string
	var foldersToCreate []string // S3 keys for folders

	err := filepath.Walk(localPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(localPath, path)
		if err != nil {
			return err
		}

		// 构造 S3 key，并确保总是使用 '/' 作为路径分隔符
		s3Key := filepath.Join(ov.currentPrefix, baseFolderName, relPath)
		s3Key = strings.ReplaceAll(s3Key, string(os.PathSeparator), "/")

		if info.IsDir() {
			foldersToCreate = append(foldersToCreate, s3Key+"/")
		} else {
			filesToUpload = append(filesToUpload, path)
		}
		return nil
	})

	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("遍历文件夹失败: %v", err), ov.window)
		})
		return
	}

	if len(filesToUpload) == 0 && len(foldersToCreate) == 0 {
		return // 路径无效或不可读
	}

	fyne.Do(func() {
		progressDialog.SetDismissText(fmt.Sprintf("准备上传 %d 个文件夹和 %d 个文件...", len(foldersToCreate), len(filesToUpload)))
	})

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failedItems []string
	numWorkers := 10

	// --- 1. 并行创建所有文件夹 ---
	if len(foldersToCreate) > 0 {
		folderChannel := make(chan string, len(foldersToCreate))
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for s3Key := range folderChannel {
					err := ov.s3Client.CreateFolder(ov.currentBucket, s3Key)
					if err != nil {
						log.Printf("创建文件夹 %s 失败: %v", s3Key, err)
						mu.Lock()
						failedItems = append(failedItems, s3Key)
						mu.Unlock()
					}
				}
			}()
		}
		for _, key := range foldersToCreate {
			folderChannel <- key
		}
		close(folderChannel)
		wg.Wait()
	}

	// --- 2. 并行上传所有文件 ---
	if len(filesToUpload) > 0 {
		uploadChannel := make(chan string, len(filesToUpload))
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for filePath := range uploadChannel {
					relPath, err := filepath.Rel(localPath, filePath)
					if err != nil {
						log.Printf("无法获取相对路径: %v", err)
						mu.Lock()
						failedItems = append(failedItems, filepath.Base(filePath))
						mu.Unlock()
						continue
					}
					s3Key := filepath.Join(ov.currentPrefix, baseFolderName, relPath)
					s3Key = strings.ReplaceAll(s3Key, string(os.PathSeparator), "/")

					file, err := os.Open(filePath)
					if err != nil {
						log.Printf("无法打开文件 %s: %v", filePath, err)
						mu.Lock()
						failedItems = append(failedItems, filepath.Base(filePath))
						mu.Unlock()
						continue
					}

					fileInfo, err := file.Stat()
					if err != nil {
						log.Printf("无法获取文件信息 %s: %v", filePath, err)
						mu.Lock()
						failedItems = append(failedItems, filepath.Base(filePath))
						mu.Unlock()
						file.Close()
						continue
					}

					err = ov.s3Client.UploadObject(ov.currentBucket, s3Key, file, fileInfo.Size())
					file.Close()
					if err != nil {
						log.Printf("上传文件 %s 失败: %v", filePath, err)
						mu.Lock()
						failedItems = append(failedItems, filepath.Base(filePath))
						mu.Unlock()
					}
				}
			}()
		}
		for _, f := range filesToUpload {
			uploadChannel <- f
		}
		close(uploadChannel)
		wg.Wait()
	}

	fyne.Do(func() {
		if len(failedItems) > 0 {
			dialog.ShowError(fmt.Errorf("部分项目上传失败: %s", strings.Join(failedItems, ", ")), ov.window)
		} else {
			dialog.ShowInformation("成功", fmt.Sprintf("文件夹 '%s' 上传完成。", baseFolderName), ov.window)
		}
		ov.loadObjects()
	})
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

// deleteFolderAndContents 递归删除文件夹及其所有内容
func (ov *ObjectsView) deleteFolderAndContents(bucket, prefix string) error {
	// 1. 列出前缀下的所有对象
	objects, err := ov.s3Client.ListAllObjectsUnderPrefix(bucket, prefix)
	if err != nil {
		return fmt.Errorf("列出文件夹 '%s' 内容失败: %w", prefix, err)
	}

	// 2. 创建要删除的键列表
	keysToDelete := make([]string, 0, len(objects)+1)
	for _, obj := range objects {
		keysToDelete = append(keysToDelete, obj.Key)
	}
	// 3. 将文件夹对象本身添加到列表
	keysToDelete = append(keysToDelete, prefix)

	// 4. 并行删除对象
	var wg sync.WaitGroup
	var mu sync.Mutex
	var deletionErrors []error

	deleteChannel := make(chan string, len(keysToDelete))
	numWorkers := 10 // 合理的并行工作者数量

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range deleteChannel {
				err := ov.s3Client.DeleteObject(bucket, key)
				if err != nil {
					mu.Lock()
					// 存储根错误以供报告
					if len(deletionErrors) == 0 {
						deletionErrors = append(deletionErrors, err)
					}
					mu.Unlock()
					log.Printf("删除对象 %s 失败: %v", key, err)
				}
			}
		}()
	}

	for _, key := range keysToDelete {
		deleteChannel <- key
	}
	close(deleteChannel)

	wg.Wait()

	if len(deletionErrors) > 0 {
		return fmt.Errorf("删除文件夹 '%s' 时发生错误，部分对象删除失败", prefix)
	}

	return nil
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
