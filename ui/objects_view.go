package ui

import (
	"bytes"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/nfnt/resize"
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

	"s3-explorer/common"
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



// --- 主视图 ---

// ObjectsView 结构体用于管理右侧的文件/文件夹列表视图
type ObjectsView struct {
	window              fyne.Window
	s3Client            *s3client.S3Client
	currentBucket       string
	currentPrefix       string
	objects             []s3client.S3Object
	filteredObjects     []s3client.S3Object // 用于存储过滤后的对象
	objectList          *widget.List
	breadcrumbContainer *fyne.Container
	selectedObjectIDs   map[widget.ListItemID]struct{}
	lastSelectedID      widget.ListItemID
	loadingIndicator    *ThinProgressBar
	downloadButton      *widget.Button
	deleteButton        *widget.Button
	serviceInfoButton   *widget.Button
	searchEntry         *widget.Entry // 搜索框

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
		loadingIndicator:  NewThinProgressBar(),
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
	fyne.Do(func() {
		if alias != "" {
			ov.serviceInfoButton.SetText(fmt.Sprintf("当前服务: %s", alias))
		} else {
			ov.serviceInfoButton.SetText("未选择服务")
		}
		ov.serviceInfoButton.Refresh()
		if ov.mainContent != nil {
			ov.mainContent.Refresh()
		}
	})
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
		nameLabel: widget.NewLabel("名称"),
		infoLabel: widget.NewLabel("大小/时间"),
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

// --- 网格条目组件 ---

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
	nameLabel := widget.NewLabel("文件名")
	nameLabel.Wrapping = fyne.TextTruncate // 修改为截断
	nameLabel.Alignment = fyne.TextAlignCenter

	entry := &gridEntry{
		icon:      icon,
		nameLabel: nameLabel,
		ov:        ov,
	}
	entry.ExtendBaseWidget(entry)
	return entry
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

	// 确保ID在有效范围内
	if id >= len(ov.getDisplayedObjects()) {
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
				// 确保索引在有效范围内
				if i < len(ov.getDisplayedObjects()) {
					ov.selectedObjectIDs[i] = struct{}{}
				}
			}
		}
	}
	ov.refreshSelection()
	ov.updateButtonsState()

	// 根据选择更新窗口标题
	if len(ov.selectedObjectIDs) == 1 {
		for selectedID := range ov.selectedObjectIDs { // 获取单个选定的ID
			items := ov.getDisplayedObjects()
			if selectedID < len(items) {
				ov.window.SetTitle(fmt.Sprintf("S3 资源管理器 ---> %s", items[selectedID].Name))
			}
		}
	} else {
		ov.window.SetTitle("S3 资源管理器") // 默认标题
	}
}

// unselectAllObjects 取消所有对象的选择
func (ov *ObjectsView) unselectAllObjects() {
	if len(ov.selectedObjectIDs) > 0 {
		ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
		ov.lastSelectedID = -1
		ov.refreshSelection()
		ov.updateButtonsState()
		ov.window.SetTitle("S3 资源管理器") // 未选择任何内容时重置标题
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
		default: // linux, freebsd, openbsd, netbsd 等
			cmd = exec.Command("xdg-open", tempFilePath)
		}

		if err := cmd.Start(); err != nil {
			log.Printf("打开外部应用失败: %v", err)
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("无法使用默认应用打开文件: %v", err), ov.window) })
		}
	}()
}

// handleDrop 处理拖放的文件和文件夹
func (ov *ObjectsView) handleDrop(uris []fyne.URI) {
	if ov.s3Client == nil || ov.currentBucket == "" {
		dialog.ShowInformation("提示", "请先选择一个 S3 服务和存储桶才能上传。", ov.window)
		return
	}
	if len(uris) == 0 {
		return
	}

	log.Printf("接收到 %d 个拖放项目", len(uris))

	// 将所有项目收集起来，统一处理
	var pathsToUpload []string
	for _, uri := range uris {
		if uri.Scheme() != "file" {
			log.Printf("跳过非文件拖放项目: %s", uri)
			continue
		}
		pathsToUpload = append(pathsToUpload, uri.Path())
	}

	if len(pathsToUpload) > 0 {
		go ov.startUploadProcess(pathsToUpload)
	}
}

// uploadSingleFile 处理单个文件的实际上传逻辑。
// 它将文件内容读入内存，然后上传到 S3。
// 这种方法使用 bytes.NewReader (io.ReadSeeker) 来避免在使用 HTTP 和校验和时出现 "unseekable stream" 错误。
func (ov *ObjectsView) uploadSingleFile(localPath, s3Key string, fileSize int64, totalOverallSize int64, bytesUploaded *int64, progressDialog *dialog.ProgressDialog) error {
	// 1. 将整个文件内容读入内存
	// 注意：对于大文件，这可能会消耗大量内存。
	data, err := ioutil.ReadFile(localPath) // ioutil.ReadFile 返回 []byte
	if err != nil {
		return fmt.Errorf("无法读取文件 '%s' 到内存: %w", filepath.Base(localPath), err)
	}

	// 2. 从字节切片创建一个 io.ReadSeeker
	reader := bytes.NewReader(data)
	actualFileSize := int64(len(data)) // 从数据重新计算文件大小

	// 3. 使用进度跟踪器包装 reader
	// bytes.NewReader 是一个 io.ReadSeeker，而我们的 ProgressTracker 包装了一个 io.Reader。
	// SDK 现在应该能够在需要时处理校验和。
	readerWithProgress := NewProgressTracker(reader, totalOverallSize, bytesUploaded, progressDialog)

	// 4. 将 io.ReadSeeker (readerWithProgress) 传递给 S3 客户端。
	err = ov.s3Client.UploadObject(ov.currentBucket, s3Key, readerWithProgress, actualFileSize)
	if err != nil {
		return fmt.Errorf("上传文件 '%s' 失败: %w", filepath.Base(localPath), err)
	}

	return nil
}

// refreshObjectView 在数据更改（加载对象）或视图模式切换时调用。
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

// refreshSelection 在项目被选中/取消选中时调用。
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
			return len(ov.getDisplayedObjects())
		},
		func() fyne.CanvasObject {
			return newListEntry(ov)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			items := ov.getDisplayedObjects()
			if id >= len(items) {
				return
			}
			
			item := items[id]
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
	displayedObjects := ov.getDisplayedObjects()
	
	for i := 0; i < len(displayedObjects); i++ {
		item := displayedObjects[i]
		entry := newGridEntry(ov)
		entry.id = i
		entry.nameLabel.SetText(formatFileNameForDisplay(item.Name, 20)) // 设置单行显示的文件名格式，包括截断和扩展名
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

	// 创建搜索框
	ov.searchEntry = widget.NewEntry()
	ov.searchEntry.SetPlaceHolder("搜索文件...")
	ov.searchEntry.OnChanged = func(s string) {
		ov.filterObjects(s)
	}

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
				go ov.startUploadProcess([]string{reader.URI().Path()})
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
				go ov.startUploadProcess([]string{uri.Path()})
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
					// --- 为删除操作进行初步扫描以获取项目总数 ---
					scanProgressDialog := dialog.NewProgressInfinite("正在准备删除", "正在扫描待删除项目...", ov.window)
					scanProgressDialog.Show()

					var totalItemsToDelete int32 = 0
					var scanErrors []error
					var scanWg sync.WaitGroup
					var scanMu sync.Mutex

					itemsToProcess := make(chan s3client.S3Object, selectedCount)

					// 使用选中的项目填充通道
					for id := range ov.selectedObjectIDs {
						items := ov.getDisplayedObjects()
						if id < len(items) {
							itemsToProcess <- items[id]
						}
					}
					close(itemsToProcess)

					// 用于扫描的工作者 goroutines
					numScanWorkers := 5 // 可根据需要调整
					for i := 0; i < numScanWorkers; i++ {
						scanWg.Add(1)
						go func() {
							defer scanWg.Done()
							for item := range itemsToProcess {
								if item.IsFolder {
									keys, err := ov.s3Client.ListAllKeysUnderPrefix(ov.currentBucket, item.Key)
									scanMu.Lock()
									if err != nil {
										scanErrors = append(scanErrors, fmt.Errorf("扫描文件夹 '%s' 失败: %w", item.Name, err))
									} else {
										totalItemsToDelete += int32(len(keys)) // 添加文件夹内的所有键
									}
									scanMu.Unlock()
								} else {
									scanMu.Lock()
									totalItemsToDelete++ // 文件本身
									scanMu.Unlock()
								}
							}
						}()
					}
					scanWg.Wait()
					fyne.Do(func() {
						scanProgressDialog.Hide()
					})

					if len(scanErrors) > 0 {
						fyne.Do(func() {
							dialog.ShowError(fmt.Errorf("扫描部分项目失败: %v", scanErrors[0]), ov.window) // 显示第一个错误
						})
						return
					}

					if totalItemsToDelete == 0 {
						fyne.Do(func() {
							dialog.ShowInformation("提示", "没有可删除的项目。", ov.window)
						})
						return
					}

					// --- 执行实际删除操作并显示进度条 ---
					deleteProgressDialog := dialog.NewProgress("正在删除", "正在删除项目...", ov.window)
					deleteProgressDialog.Show()

					var currentDeletedItems int32 = 0
					var deletionWg sync.WaitGroup
					var deletionMu sync.Mutex
					var failedDeletions []string

					// 用于删除项目的通道（可以是文件或文件夹）
					itemsToDeleteChannel := make(chan s3client.S3Object, selectedCount)
					for id := range ov.selectedObjectIDs {
						items := ov.getDisplayedObjects()
						if id < len(items) {
							itemsToDeleteChannel <- items[id]
						}
					}
					close(itemsToDeleteChannel)

					numDeleteWorkers := 10 // 根据需要进行调整
					for i := 0; i < numDeleteWorkers; i++ {
						deletionWg.Add(1)
						go func() {
							defer deletionWg.Done()
							for selectedObject := range itemsToDeleteChannel {
								var err error
								if selectedObject.IsFolder {
									s3Prefix := selectedObject.Key
									if !strings.HasSuffix(s3Prefix, "/") {
										s3Prefix += "/"
									}
									// 调用更新进度的新函数
									err = ov.deleteFolderAndContentsWithProgress(ov.currentBucket, s3Prefix, &currentDeletedItems, &deletionMu, deleteProgressDialog, totalItemsToDelete)
								} else {
									err = ov.s3Client.DeleteObject(ov.currentBucket, selectedObject.Key)
									deletionMu.Lock()
									currentDeletedItems++
									fyne.Do(func() { deleteProgressDialog.SetValue(float64(currentDeletedItems) / float64(totalItemsToDelete)) })
									deletionMu.Unlock()
								}

								if err != nil {
									deletionMu.Lock()
									failedDeletions = append(failedDeletions, selectedObject.Name)
									deletionMu.Unlock()
									log.Printf("删除项目 '%s' 失败: %v", selectedObject.Name, err)
								}
							}
						}()
					}
					deletionWg.Wait()
					fyne.Do(func() {
						deleteProgressDialog.Hide()
					})

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

	topBar := container.NewBorder(nil, nil, ov.breadcrumbContainer, fileOpsButtons, ov.searchEntry)

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
	ov.refreshObjectView() // 初始视图

	// 创建一个用于裁剪进度条的滚动容器
	clippedProgressBar := container.NewScroll(ov.loadingIndicator)
	clippedProgressBar.SetMinSize(fyne.NewSize(0, ov.loadingIndicator.MinSize().Height))

	// 将主内容区和裁剪后的加载指示器放入一个堆栈容器中
	contentWithProgressBar := container.NewStack(ov.mainContent, clippedProgressBar)
	clippedProgressBar.Move(fyne.NewPos(0, 0)) // 手动定位到顶部

	centerContent := container.NewVBox(
		widget.NewSeparator(),
	)

	return container.NewBorder(topBar, statusBar, nil, nil, centerContent, contentWithProgressBar)
}

// startUploadProcess 启动上传流程 (文件或文件夹)
func (ov *ObjectsView) startUploadProcess(localPaths []string) {
	scanProgressDialog := dialog.NewProgressInfinite("正在准备上传", "正在扫描文件...", ov.window)
	fyne.Do(func() {
		scanProgressDialog.Show()
	})

	var totalSize int64
	var filesToUpload []struct {
		LocalPath string
		S3Key     string
		Size      int64
	}
	var foldersToCreate []string // 用于创建文件夹的 S3 key
	var scanErrors []error
	var scanWg sync.WaitGroup
	var scanMu sync.Mutex

	// 步骤 1: 扫描所有文件并计算总大小
	for _, localPath := range localPaths {
		scanWg.Add(1)
		go func(path string) {
			defer scanWg.Done()
			info, err := os.Stat(path)
			if err != nil {
				scanMu.Lock()
				scanErrors = append(scanErrors, fmt.Errorf("无法获取项目信息 '%s': %w", filepath.Base(path), err))
				scanMu.Unlock()
				return
			}

			if info.IsDir() {
				baseFolderName := filepath.Base(path)
				err := filepath.Walk(path, func(p string, i os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					relPath, err := filepath.Rel(path, p)
					if err != nil {
						return err
					}
					s3Key := filepath.Join(ov.currentPrefix, baseFolderName, relPath)
					s3Key = strings.ReplaceAll(s3Key, string(os.PathSeparator), "/")

					scanMu.Lock()
					if i.IsDir() {
						foldersToCreate = append(foldersToCreate, s3Key+"/")
					} else {
						filesToUpload = append(filesToUpload, struct {
							LocalPath string
							S3Key     string
							Size      int64
						}{LocalPath: p, S3Key: s3Key, Size: i.Size()})
						totalSize += i.Size()
					}
					scanMu.Unlock()
					return nil
				})
				if err != nil {
					scanMu.Lock()
					scanErrors = append(scanErrors, fmt.Errorf("遍历文件夹 '%s' 失败: %w", filepath.Base(path), err))
					scanMu.Unlock()
				}
			} else {
				fileName := filepath.Base(path)
				s3Key := ov.currentPrefix + fileName
				scanMu.Lock()
				filesToUpload = append(filesToUpload, struct {
					LocalPath string
					S3Key     string
					Size      int64
				}{LocalPath: path, S3Key: s3Key, Size: info.Size()})
				totalSize += info.Size()
				scanMu.Unlock()
			}
		}(localPath)
	}
	scanWg.Wait()
	fyne.Do(func() {
		scanProgressDialog.Hide()
	})

	if len(scanErrors) > 0 {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("扫描部分项目失败: %s", scanErrors[0].Error()), ov.window)
		})
		return
	}

	if len(filesToUpload) == 0 && len(foldersToCreate) == 0 {
		fyne.Do(func() {
			dialog.ShowInformation("提示", "没有可上传的项目。", ov.window)
		})
		return
	}

	// 步骤 2: 执行上传并显示进度条
	uploadProgressDialog := dialog.NewProgress("正在上传", "正在上传项目...", ov.window)
	fyne.Do(func() {
		uploadProgressDialog.Show()
	})

	var bytesUploaded int64
	var uploadWg sync.WaitGroup
	var uploadMu sync.Mutex
	var failedUploads []string
	numWorkers := 10

	// 1. 并行创建所有文件夹
	if len(foldersToCreate) > 0 {
		folderChannel := make(chan string, len(foldersToCreate))
		for i := 0; i < numWorkers; i++ {
			uploadWg.Add(1)
			go func() {
				defer uploadWg.Done()
				for s3Key := range folderChannel {
					err := ov.s3Client.CreateFolder(ov.currentBucket, s3Key)
					if err != nil {
						log.Printf("创建文件夹 %s 失败: %v", s3Key, err)
						uploadMu.Lock()
						failedUploads = append(failedUploads, s3Key)
						uploadMu.Unlock()
					}
				}
			}()
		}
		for _, key := range foldersToCreate {
			folderChannel <- key
		}
		close(folderChannel)
		uploadWg.Wait() // 等待文件夹创建完成后再上传文件
	}

	// 2. 并行上传所有文件
	if len(filesToUpload) > 0 {
		fileChannel := make(chan struct {
			LocalPath string
			S3Key     string
			Size      int64
		}, len(filesToUpload))

		for i := 0; i < numWorkers; i++ {
			uploadWg.Add(1)
			go func() {
				defer uploadWg.Done()
				for fileInfo := range fileChannel {
					err := ov.uploadSingleFile(fileInfo.LocalPath, fileInfo.S3Key, fileInfo.Size, totalSize, &bytesUploaded, uploadProgressDialog)
					if err != nil {
						uploadMu.Lock()
						failedUploads = append(failedUploads, filepath.Base(fileInfo.LocalPath))
						uploadMu.Unlock()
						log.Printf("上传文件 %s 失败: %v", fileInfo.LocalPath, err)
					}
				}
			}()
		}
		for _, f := range filesToUpload {
			fileChannel <- f
		}
		close(fileChannel)
		uploadWg.Wait()
	}

	fyne.Do(func() {
		uploadProgressDialog.Hide()
	})

	fyne.Do(func() {
		if len(failedUploads) > 0 {
			const maxDisplayedFailures = 5
			displayMessage := "部分项目上传失败: "
			if len(failedUploads) > maxDisplayedFailures {
				displayMessage += strings.Join(failedUploads[:maxDisplayedFailures], ", ") + fmt.Sprintf(" 等 %d 个文件", len(failedUploads))
			} else {
				displayMessage += strings.Join(failedUploads, ", ")
			}
			dialog.ShowError(fmt.Errorf(displayMessage), ov.window)
		} else {
			dialog.ShowInformation("成功", "所有项目上传完成。", ov.window)
		}
		ov.loadObjects()
	})
}

// startDownloadProcess 启动下载流程
func (ov *ObjectsView) startDownloadProcess(localBasePath string) {
	scanProgressDialog := dialog.NewProgressInfinite("正在准备下载", "正在扫描待下载项目...", ov.window)
	scanProgressDialog.Show()

	var totalDownloadSize int64
	var filesToDownload []struct {
		S3Object  s3client.S3Object
		LocalPath string
	}
	var scanErrors []error
	var scanWg sync.WaitGroup
	var scanMu sync.Mutex

	// 步骤 1: 扫描所有选中的项目以确定总大小和要下载的文件
	objectsToScan := make(chan s3client.S3Object, len(ov.selectedObjectIDs))
	for id := range ov.selectedObjectIDs {
		items := ov.getDisplayedObjects()
		if id < len(items) {
			objectsToScan <- items[id]
		}
	}
	close(objectsToScan)

	numScanWorkers := 5 // 根据需要进行调整
	for i := 0; i < numScanWorkers; i++ {
		scanWg.Add(1)
		go func() {
			defer scanWg.Done()
			for obj := range objectsToScan {
				if obj.IsFolder {
					// 列出前缀下的所有对象以获取它们的大小
					folderObjects, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, obj.Key)
					scanMu.Lock()
					if err != nil {
						scanErrors = append(scanErrors, fmt.Errorf("扫描文件夹 '%s' 失败: %w", obj.Name, err))
					} else {
						for _, fo := range folderObjects {
							if !fo.IsFolder { // Only count files
								totalDownloadSize += fo.Size
								relativePath := strings.TrimPrefix(fo.Key, obj.Key)
								localFilePath := filepath.Join(localBasePath, obj.Name, relativePath)
								filesToDownload = append(filesToDownload, struct {
									S3Object  s3client.S3Object
									LocalPath string
								}{S3Object: fo, LocalPath: localFilePath})
							}
						}
					}
					scanMu.Unlock()
				} else {
					scanMu.Lock()
					totalDownloadSize += obj.Size
					localFilePath := filepath.Join(localBasePath, obj.Name)
					filesToDownload = append(filesToDownload, struct {
						S3Object  s3client.S3Object
						LocalPath string
					}{S3Object: obj, LocalPath: localFilePath})
					scanMu.Unlock()
				}
			}
		}()
	}
	scanWg.Wait()
	fyne.Do(func() {
		scanProgressDialog.Hide()
	})

	if len(scanErrors) > 0 {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("扫描部分项目失败: %s", scanErrors[0].Error()), ov.window)
		})
		return
	}

	if len(filesToDownload) == 0 {
		fyne.Do(func() {
			dialog.ShowInformation("提示", "没有可下载的项目。", ov.window)
		})
		return
	}

	// 步骤 2: 执行下载并显示进度条
	downloadProgressDialog := dialog.NewProgress("正在下载", "正在下载项目...", ov.window)
	downloadProgressDialog.Show()

	var bytesDownloaded int64
	var downloadWg sync.WaitGroup
	var downloadMu sync.Mutex
	var failedDownloads []string
	numDownloadWorkers := 10

	downloadChannel := make(chan struct {
		S3Object  s3client.S3Object
		LocalPath string
	}, len(filesToDownload))

	for i := 0; i < numDownloadWorkers; i++ {
		downloadWg.Add(1)
		go func() {
			defer downloadWg.Done()
			for fileInfo := range downloadChannel {
				err := ov.downloadFile(fileInfo.S3Object, fileInfo.LocalPath, totalDownloadSize, &bytesDownloaded, downloadProgressDialog)
				if err != nil {
					downloadMu.Lock()
					failedDownloads = append(failedDownloads, fileInfo.S3Object.Name)
					downloadMu.Unlock()
					log.Printf("下载文件 '%s' 失败: %v", fileInfo.S3Object.Name, err)
				}
			}
		}()
	}

	for _, f := range filesToDownload {
		downloadChannel <- f
	}
	close(downloadChannel)

	downloadWg.Wait()
	fyne.Do(func() {
		downloadProgressDialog.Hide()
	})

	fyne.Do(func() {
		if len(failedDownloads) > 0 {
			dialog.ShowError(fmt.Errorf("部分项目下载失败: %s", strings.Join(failedDownloads, ", ")), ov.window)
		} else {
			dialog.ShowInformation("成功", "所有项目下载完成。", ov.window)
		}
		ov.loadObjects()
	})
}

// processDownloadItem 处理单个项目（文件或文件夹）的下载
func (ov *ObjectsView) processDownloadItem(obj s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex, totalDownloadSize int64, bytesDownloaded *int64, downloadProgressDialog *dialog.ProgressDialog) {
	if obj.IsFolder {
		ov.downloadFolder(obj, localBasePath, failedDownloads, mu, totalDownloadSize, bytesDownloaded, downloadProgressDialog)
	} else {
		// 对于单个文件，错误会直接从 downloadFile 返回
		err := ov.downloadFile(obj, localBasePath, totalDownloadSize, bytesDownloaded, downloadProgressDialog)
		if err != nil {
			mu.Lock()
			*failedDownloads = append(*failedDownloads, obj.Name)
			mu.Unlock()
			log.Printf("下载文件 '%s' 失败: %v", obj.Name, err)
		}
	}
}

// downloadFile 下载单个文件
func (ov *ObjectsView) downloadFile(obj s3client.S3Object, localPath string, totalSize int64, bytesDownloaded *int64, progressDialog *dialog.ProgressDialog) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}

	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer localFile.Close()

	body, err := ov.s3Client.DownloadObject(ov.currentBucket, obj.Key)
	if err != nil {
		return fmt.Errorf("从 S3 下载失败: %w", err)
	}
	defer body.Close()

	// 使用进度跟踪器包装 S3 下载的数据流
	readerWithProgress := NewProgressTracker(body, totalSize, bytesDownloaded, progressDialog)

	_, err = io.Copy(localFile, readerWithProgress)
	if err != nil {
		return fmt.Errorf("写入本地文件失败: %w", err)
	}
	return nil
}

// downloadFolder 递归下载文件夹
func (ov *ObjectsView) downloadFolder(folder s3client.S3Object, localBasePath string, failedDownloads *[]string, mu *sync.Mutex, totalDownloadSize int64, bytesDownloaded *int64, downloadProgressDialog *dialog.ProgressDialog) {
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
			// 传递所有与进度相关的参数
			err := ov.downloadFile(fileToDownload, localPath, totalDownloadSize, bytesDownloaded, downloadProgressDialog)
			if err != nil {
				mu.Lock()
				*failedDownloads = append(*failedDownloads, fileToDownload.Name)
				mu.Unlock()
				log.Printf("下载文件 '%s' 失败: %v", fileToDownload.Name, err)
			}
		}(obj)
	}
	wg.Wait()
}

// deleteFolderAndContents 递归删除文件夹及其所有内容
func (ov *ObjectsView) deleteFolderAndContents(bucket, prefix string) error {
	// 1. 列出前缀下的所有对象键（包括文件和文件夹标记）
	keys, err := ov.s3Client.ListAllKeysUnderPrefix(bucket, prefix)
	if err != nil {
		return fmt.Errorf("列出文件夹 '%s' 内容失败: %w", prefix, err)
	}

	// 2. 创建要删除的键列表
	keysToDelete := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		keysToDelete = append(keysToDelete, key)
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

// deleteFolderAndContentsWithProgress 递归删除文件夹及其所有内容，并更新进度
func (ov *ObjectsView) deleteFolderAndContentsWithProgress(bucket, prefix string, currentDeletedItems *int32, mu *sync.Mutex, progressDialog *dialog.ProgressDialog, totalItemsToDelete int32) error {
	keys, err := ov.s3Client.ListAllKeysUnderPrefix(bucket, prefix)
	if err != nil {
		return fmt.Errorf("列出文件夹 '%s' 内容失败: %w", prefix, err)
	}

	// 将文件夹对象本身添加到待删除键的列表中
	keysToDelete := append(keys, prefix)

	var wg sync.WaitGroup
	var deletionErrors []error

	deleteChannel := make(chan string, len(keysToDelete))
	numWorkers := 10

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range deleteChannel {
				err := ov.s3Client.DeleteObject(bucket, key)
				if err != nil {
					mu.Lock()
					if len(deletionErrors) == 0 { // 仅存储第一个错误
						deletionErrors = append(deletionErrors, err)
					}
					mu.Unlock()
					log.Printf("删除对象 %s 失败: %v", key, err)
				} else {
					mu.Lock()
					*currentDeletedItems++
					fyne.Do(func() { progressDialog.SetValue(float64(*currentDeletedItems) / float64(totalItemsToDelete)) })
					mu.Unlock()
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
	switch common.GetIconForFile(name) {
	case "image":
		return theme.FileImageIcon()
	case "audio":
		return theme.FileAudioIcon()
	case "video":
		return theme.FileVideoIcon()
	case "text":
		return theme.FileTextIcon()
	default:
		return theme.FileIcon()
	}
}

func isPreviewableImage(name string) bool {
	return common.IsPreviewableImage(name)
}

// formatFileNameForDisplay 格式化文件名，确保单行显示，过长则截断并保留后缀
func formatFileNameForDisplay(fileName string, maxDisplayLength int) string {
	return common.FormatFileNameForDisplay(fileName, maxDisplayLength)
}

// formatBytes 格式化字节大小为可读的字符串
func formatBytes(b int64) string {
	return common.FormatBytes(b)
}

// filterObjects 根据搜索词过滤对象列表
func (ov *ObjectsView) filterObjects(searchTerm string) {
	if searchTerm == "" {
		// 如果搜索词为空，显示所有对象
		ov.filteredObjects = nil
	} else {
		// 过滤对象列表
		ov.filteredObjects = make([]s3client.S3Object, 0)
		searchTerm = strings.ToLower(searchTerm)
		
		for _, obj := range ov.objects {
			// 将对象名称转换为小写进行不区分大小写的搜索
			if strings.Contains(strings.ToLower(obj.Name), searchTerm) {
				ov.filteredObjects = append(ov.filteredObjects, obj)
			}
		}
	}
	
	// 重置选择状态
	ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
	ov.lastSelectedID = -1
	ov.updateButtonsState()
	
	// 刷新视图
	ov.refreshObjectView()
}

// getDisplayedObjects 返回当前应该显示的对象列表（过滤后或全部）
func (ov *ObjectsView) getDisplayedObjects() []s3client.S3Object {
	if ov.filteredObjects != nil {
		return ov.filteredObjects
	}
	return ov.objects
}
