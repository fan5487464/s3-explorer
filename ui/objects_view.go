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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time" // 导入 time 包用于动画

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/nfnt/resize"

	"s3-explorer/common"
	"s3-explorer/s3client"
)

// --- 全局缓存与自定义类型 ---
var (
	thumbnailCache = make(map[string]fyne.Resource)
	cacheLock      = sync.RWMutex{}

	// 用于存储复制的对象信息
	copiedObjects     []s3client.S3Object
	copiedObjectsLock = sync.RWMutex{}

	// 用于跟踪最后一次复制操作的时间和类型
	lastCopyTime time.Time
	lastCopyType string // "s3" 或 "system"
	copyTimeLock = sync.RWMutex{}
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

	// 动画管理器
	animationManager *AnimationManager

	// OnViewModeChanged 是一个回调函数，当视图模式改变时触发
	OnViewModeChanged func(alias, newMode string)
}

// NewObjectsView 创建并返回一个新的 ObjectsView 实例
func NewObjectsView(w fyne.Window, am *AnimationManager) *ObjectsView { // 修改函数签名
	ov := &ObjectsView{
		window:            w,
		animationManager:  am, // 初始化动画管理器
		selectedObjectIDs: make(map[widget.ListItemID]struct{}),
		lastSelectedID:    -1,
		loadingIndicator:  NewThinProgressBar(),
		serviceInfoButton: widget.NewButton("未选择服务", func() {}),
		currentPage:       1,
		pageSize:          100, // 0 表示不限制
		pageMarkers:       []string{""},
		viewMode:          listViewMode, // 默认是列表视图
	}
	ov.serviceInfoButton.Importance = widget.LowImportance
	ov.serviceInfoButton.Disable()
	ov.loadingIndicator.Hide()

	ov.window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		ov.handleDrop(uris)
	})

	// 注册键盘快捷键处理
	ov.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(shortcut fyne.Shortcut) {
		ov.handleCopy()
	})

	ov.window.Canvas().AddShortcut(&fyne.ShortcutPaste{}, func(shortcut fyne.Shortcut) {
		ov.handlePaste()
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
	ov.pageMarkers = []string{""} // 重置为初始状态
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
		var objects []s3client.S3Object
		var nextMarker *string
		var err error

		if ov.pageSize == 0 {
			// 不限制分页，获取所有对象
			objects, err = ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, ov.currentPrefix)
			if err != nil {
				log.Printf("列出所有对象失败: %v", err)
			}
			// 不分页时不需要 nextMarker
		} else {
			// 使用分页
			// 对于第一页，marker应该是空字符串
			var marker string
			if ov.currentPage == 1 {
				marker = ""
			} else if ov.currentPage <= len(ov.pageMarkers) {
				marker = ov.pageMarkers[ov.currentPage-1]
			} else {
				// 这种情况不应该发生，但为了安全起见
				marker = ""
			}
			objects, nextMarker, err = ov.s3Client.ListObjects(ov.currentBucket, ov.currentPrefix, marker, int32(ov.pageSize))
		}

		fyne.Do(func() {
			ov.loadingIndicator.Hide()
			if err != nil {
				log.Printf("列出对象失败: %v", err)
				dialog.ShowError(fmt.Errorf("列出对象失败: %v", err), ov.window)
				ov.objects = []s3client.S3Object{}
			} else {
				ov.objects = objects
				ov.nextPageMarker = nextMarker
				// 只有在分页模式下才更新pageMarkers
				if ov.pageSize != 0 && nextMarker != nil {
					// 确保pageMarkers数组足够长
					if len(ov.pageMarkers) < ov.currentPage+1 {
						ov.pageMarkers = append(ov.pageMarkers, make([]string, ov.currentPage+1-len(ov.pageMarkers))...)
					}
					// 更新下一页的marker
					ov.pageMarkers[ov.currentPage] = *nextMarker
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
		// 处理右键点击，显示上下文菜单
		ov.showContextMenu(id, m)
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

// showContextMenu 显示右键菜单
func (ov *ObjectsView) showContextMenu(id widget.ListItemID, m *desktop.MouseEvent) {
	// 确保ID在有效范围内
	if id >= len(ov.getDisplayedObjects()) {
		return
	}

	// 检查该项目是否已被选中
	_, alreadySelected := ov.selectedObjectIDs[id]
	
	// 如果点击的项目未被选中，则选中它
	if !alreadySelected {
		// 清除其他选择，只选择当前项目
		ov.selectedObjectIDs = make(map[widget.ListItemID]struct{})
		ov.selectedObjectIDs[id] = struct{}{}
		ov.lastSelectedID = id
		ov.refreshSelection()
		ov.updateButtonsState()
	}

	// 获取选中的对象
	items := ov.getDisplayedObjects()
	var selectedObjects []s3client.S3Object
	for selectedID := range ov.selectedObjectIDs {
		if selectedID < len(items) {
			selectedObjects = append(selectedObjects, items[selectedID])
		}
	}

	// 创建菜单项
	var menuItems []*fyne.MenuItem

	// 如果只选中了一个项目
	if len(selectedObjects) == 1 {
		obj := selectedObjects[0]
		if obj.IsFolder {
			// 文件夹菜单项
			openItem := fyne.NewMenuItem("打开", func() {
				ov.SetBucketAndPrefix(ov.s3Client, ov.currentBucket, obj.Key)
			})
			openItem.Icon = theme.FolderOpenIcon()
			menuItems = append(menuItems, openItem)
		} else {
			// 文件菜单项
			openItem := fyne.NewMenuItem("打开", func() {
				ov.showPreviewWindow(obj)
			})
			openItem.Icon = theme.FileImageIcon() // 使用更通用的图标
			menuItems = append(menuItems, openItem)
			
			downloadItem := fyne.NewMenuItem("下载", func() {
				// 使用系统文件管理器选择下载目录
				go ov.openSystemFolderSelector()
			})
			downloadItem.Icon = theme.DownloadIcon()
			menuItems = append(menuItems, downloadItem)
			
			// 添加分隔线
			menuItems = append(menuItems, fyne.NewMenuItemSeparator())
		}
		
		copyItem := fyne.NewMenuItem("复制", func() {
			ov.handleCopy()
		})
		copyItem.Icon = theme.ContentCopyIcon()
		menuItems = append(menuItems, copyItem)
	} else if len(selectedObjects) > 1 {
		// 多个项目选中
		downloadItem := fyne.NewMenuItem("下载", func() {
			// 使用系统文件管理器选择下载目录
			go ov.openSystemFolderSelector()
		})
		downloadItem.Icon = theme.DownloadIcon()
		menuItems = append(menuItems, downloadItem)
		
		copyItem := fyne.NewMenuItem("复制", func() {
			ov.handleCopy()
		})
		copyItem.Icon = theme.ContentCopyIcon()
		menuItems = append(menuItems, copyItem)
		
		// 添加分隔线
		menuItems = append(menuItems, fyne.NewMenuItemSeparator())
	} else {
		// 没有选中项目时也添加分隔线占位符
		menuItems = append(menuItems, fyne.NewMenuItemSeparator())
	}

	// 添加粘贴选项（总是显示）
	pasteItem := fyne.NewMenuItem("粘贴", func() {
		ov.handlePaste()
	})
	pasteItem.Icon = theme.ContentPasteIcon()
	menuItems = append(menuItems, pasteItem)

	// 添加分隔线
	menuItems = append(menuItems, fyne.NewMenuItemSeparator())

	// 添加删除选项
	if len(selectedObjects) > 0 {
		deleteItem := fyne.NewMenuItem("删除", func() {
			if len(ov.selectedObjectIDs) == 0 {
				ShowToast(ov.window, "请先选择要删除的文件或文件夹。")
				return
			}

			dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除选中的 %d 个项目吗？", len(ov.selectedObjectIDs)), func(confirmed bool) {
				if confirmed {
					go func() {
						// --- 为删除操作进行初步扫描以获取项目总数 ---
						scanProgressDialog := dialog.NewProgressInfinite("正在准备删除", "正在扫描待删除项目...", ov.window)
						scanProgressDialog.Show()

						var totalItemsToDelete int32 = 0
						var scanErrors []error
						var scanWg sync.WaitGroup
						var scanMu sync.Mutex

						itemsToProcess := make(chan s3client.S3Object, len(ov.selectedObjectIDs))

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
						itemsToDeleteChannel := make(chan s3client.S3Object, len(ov.selectedObjectIDs))
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
								ShowToast(ov.window, fmt.Sprintf("%d 个项目已成功删除。", len(ov.selectedObjectIDs)))
							}
							ov.resetPagingAndSelection()
							ov.loadObjects()
						})
					}()
				}
			}, ov.window)
		})
		deleteItem.Icon = theme.DeleteIcon()
		menuItems = append(menuItems, deleteItem)
	}

	// 创建并显示菜单
	menu := fyne.NewMenu("", menuItems...)
	
	// 创建弹出菜单并自定义样式
	popUpMenu := widget.NewPopUpMenu(menu, ov.window.Canvas())
	
	// 设置菜单位置
	popUpMenu.ShowAtPosition(m.AbsolutePosition)
	
	// 可以通过动画管理器添加一些效果
	if ov.animationManager != nil {
		// 添加淡入效果
		ov.animationManager.AnimateFade(popUpMenu, time.Millisecond*200, 0.0, 1.0, nil)
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

// handleCopy 处理复制操作，将选中的对象信息保存到应用内部
func (ov *ObjectsView) handleCopy() {
	if len(ov.selectedObjectIDs) == 0 {
		return
	}

	// 创建要复制的内容（对象信息列表）
	var objectsToCopy []s3client.S3Object
	items := ov.getDisplayedObjects()

	for id := range ov.selectedObjectIDs {
		if id < len(items) {
			objectsToCopy = append(objectsToCopy, items[id])
		}
	}

	if len(objectsToCopy) > 0 {
		// 保存复制的对象信息到全局变量
		copiedObjectsLock.Lock()
		copiedObjects = objectsToCopy
		copiedObjectsLock.Unlock()

		// 记录复制操作的时间和类型
		copyTimeLock.Lock()
		lastCopyTime = time.Now()
		lastCopyType = "s3"
		copyTimeLock.Unlock()

		// 显示提示信息
		var message string
		if len(objectsToCopy) == 1 {
			message = fmt.Sprintf("已复制: %s", objectsToCopy[0].Name)
		} else {
			message = fmt.Sprintf("已复制 %d 个项目", len(objectsToCopy))
		}
		ShowToast(ov.window, message)
	}
}

// handlePaste 处理粘贴操作，从剪贴板获取内容并执行相应操作
func (ov *ObjectsView) handlePaste() {
	if ov.s3Client == nil || ov.currentBucket == "" {
		ShowToast(ov.window, "请先选择一个 S3 服务和存储桶。")
		return
	}

	// 首先尝试从Windows HDROP格式读取文件路径
	filePaths, err := getFilePathsFromClipboard()
	if err != nil {
		log.Printf("从Windows剪贴板读取文件路径时出错: %v", err)
	}

	// 如果Windows HDROP读取失败或没有文件路径，尝试使用Fyne的剪贴板API
	if len(filePaths) == 0 {
		// 从剪贴板获取内容
		content := ov.window.Clipboard().Content()
		if content != "" {
			log.Printf("粘贴操作: 剪贴板内容长度=%d", len(content))
			log.Printf("剪贴板内容 (前1000字符): %s", func() string {
				if len(content) > 1000 {
					return content[:1000] + "...(truncated)"
				}
				return content
			}())

			// 解析文件路径 - 支持多种格式

			// 方法1: 处理 file:// URL格式 (Windows/Linux/Mac)
			if strings.Contains(content, "file://") {
				log.Printf("检测到 file:// 格式的内容")
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "file://") {
						log.Printf("处理行: %s", line)
						// 移除 file:// 前缀并解码URL
						path := strings.TrimPrefix(line, "file://")
						// 处理Windows路径 (file:///C:/path -> C:\path)
						if len(path) > 2 && path[0] == '/' && path[2] == ':' {
							path = path[1:] // 移除开头的斜杠
						}
						decodedPath, err := url.QueryUnescape(path)
						if err != nil {
							// 如果解码失败，直接使用原始路径
							decodedPath = path
							log.Printf("URL解码失败，使用原始路径: %s", path)
						}
						filePaths = append(filePaths, decodedPath)
						log.Printf("解析到文件路径 (file://): %s", decodedPath)
					}
				}
			}

			// 方法2: 处理纯文本路径格式 (Windows)
			if len(filePaths) == 0 {
				log.Printf("未检测到 file:// 格式，尝试处理纯文本路径")
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					log.Printf("处理行: '%s'", line)
					// 检查是否为有效的Windows文件路径 (C:\path 或 D:\path 等)
					if len(line) > 3 && line[1] == ':' && (line[2] == '\\' || line[2] == '/') {
						filePaths = append(filePaths, line)
						log.Printf("解析到Windows文件路径: %s", line)
					}
				}
			}

			// 方法3: 处理Unix路径格式
			if len(filePaths) == 0 {
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					log.Printf("处理Unix路径行: '%s'", line)
					// 检查是否为有效的Unix文件路径 (/path)
					if len(line) > 1 && line[0] == '/' {
						filePaths = append(filePaths, line)
						log.Printf("解析到Unix文件路径: %s", line)
					}
				}
			}

			// 方法4: 简单处理 - 将整个剪贴板内容作为单个路径 (如果它看起来像一个路径)
			if len(filePaths) == 0 {
				content = strings.TrimSpace(content)
				log.Printf("尝试将整个剪贴板内容作为路径: '%s'", content)
				// 检查是否为有效的文件路径
				if (len(content) > 3 && content[1] == ':' && (content[2] == '\\' || content[2] == '/')) || // Windows路径
					(len(content) > 1 && content[0] == '/') { // Unix路径
					filePaths = append(filePaths, content)
					log.Printf("将整个剪贴板内容作为文件路径: %s", content)
				}
			}
		}
	}

	// 检查是否有从S3复制的对象
	copiedObjectsLock.RLock()
	localCopiedObjects := make([]s3client.S3Object, len(copiedObjects))
	copy(localCopiedObjects, copiedObjects)
	hasCopiedObjects := len(copiedObjects) > 0
	copiedObjectsLock.RUnlock()

	// 获取最后一次复制操作的信息
	copyTimeLock.RLock()
	lastCopy := lastCopyTime
	copyType := lastCopyType
	copyTimeLock.RUnlock()

	// 判断应该使用哪种复制内容
	useSystemClipboard := len(filePaths) > 0
	useS3Objects := hasCopiedObjects

	// 如果两种复制内容都存在，比较时间以确定使用哪个
	if useSystemClipboard && useS3Objects {
		// 检查系统剪贴板内容是否是最新的（通过检查内容是否在最近1秒内发生变化）
		// 这是一个简单的启发式方法，因为我们无法直接获取系统剪贴板的更改时间
		systemClipboardTime := time.Now() // 假设系统剪贴板内容是最新的

		// 如果S3复制时间晚于系统剪贴板时间，则使用S3对象
		if lastCopy.After(systemClipboardTime.Add(-1*time.Second)) && copyType == "s3" {
			useSystemClipboard = false
		} else {
			// 否则使用系统剪贴板（默认行为）
			useS3Objects = false
		}
	}

	// 如果从系统剪贴板获取到了文件路径，则上传这些文件
	if useSystemClipboard {
		log.Printf("开始上传 %d 个文件: %v", len(filePaths), filePaths)
		// 开始上传过程
		go ov.startUploadProcess(filePaths)
		return
	}

	// 如果有从S3复制的对象，执行S3到S3的复制
	if useS3Objects {
		dialog.ShowConfirm("确认粘贴", fmt.Sprintf("是否要粘贴 %d 个已复制的对象到当前目录？", len(localCopiedObjects)),
			func(confirmed bool) {
				if confirmed {
					go ov.pasteS3Objects(localCopiedObjects)
				}
			}, ov.window)
		return
	}

	// 无法识别剪贴板内容格式
	log.Printf("无法识别剪贴板内容格式")
	ShowToast(ov.window, "剪贴板中没有可识别的文件路径。")
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

	// 如果 pageSize 为 0，表示不限制分页
	if ov.pageSize == 0 {
		ov.pageInfoLabel.SetText("无分页")
		ov.prevButton.Disable()
		ov.nextButton.Disable()
	} else {
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

	// 添加淡入动画效果
	if ov.animationManager != nil {
		// 创建一个覆盖整个内容区域的半透明渐变矩形
		// 使用更柔和的颜色和更好的透明度
		fadeOverlay := canvas.NewRectangle(color.NRGBA{R: 200, G: 200, B: 200, A: 150}) // 柔和的灰色半透明
		fadeOverlay.Resize(ov.mainContent.Size())

		// 将覆盖层添加到 mainContent 的顶部
		ov.mainContent.Add(fadeOverlay)

		// 使用 AnimationManager 执行淡出动画（使覆盖层变透明，内容逐渐显现）
		// 增加动画时间使其更平滑
		ov.animationManager.AnimateFade(fadeOverlay, time.Millisecond*500, 1.0, 0.0, func() {
			// 动画结束后移除覆盖层
			ov.mainContent.Remove(fadeOverlay)
		})
	}
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
		// 动画结束后执行的逻辑
		if ov.s3Client == nil || ov.currentBucket == "" {
			ShowToast(ov.window, "请先选择一个 S3 服务和存储桶。")
			return
		}

		// 创建自定义弹窗以更好地控制尺寸
		folderNameEntry := widget.NewEntry()
		folderNameEntry.SetPlaceHolder("请输入文件夹名称")

		formContent := container.NewVBox(
			widget.NewLabel("文件夹名称:"),
			folderNameEntry,
			layout.NewSpacer(),
		)

		// 创建自定义对话框
		createFolderDialog := dialog.NewCustomConfirm("创建新文件夹", "创建", "取消", formContent, func(confirmed bool) {
			if confirmed {
				folderName := folderNameEntry.Text
				if folderName == "" {
					ShowToast(ov.window, "文件夹名称不能为空。")
					return
				}
				s3Key := ov.currentPrefix + folderName + "/"

				go func() {
					err := ov.s3Client.CreateFolder(ov.currentBucket, s3Key)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("创建文件夹失败: %v", err), ov.window)
						} else {
							ShowToast(ov.window, fmt.Sprintf("文件夹 '%s' 创建成功！", folderName))
							ov.loadObjects()
						}
					})
				}()
			}
		}, ov.window)
		createFolderDialog.Resize(fyne.NewSize(400, 200)) // 增大弹窗尺寸
		createFolderDialog.Show()
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalCreateFolderButtonOnTapped := createFolderButton.OnTapped
		createFolderButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(createFolderButton, func() {
				if originalCreateFolderButtonOnTapped != nil {
					originalCreateFolderButtonOnTapped()
				}
			})
		}
	}

	uploadButton := widget.NewButtonWithIcon("", theme.UploadIcon(), func() {
		// 动画结束后执行的逻辑
		if ov.s3Client == nil || ov.currentBucket == "" {
			ShowToast(ov.window, "请先选择一个 S3 服务和存储桶。")
			return
		}

		// 创建更美观的上传选项弹窗
		fileUploadFunc := func() {
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
			fd.SetFilter(storage.NewExtensionFileFilter([]string{})) // 不限制文件类型
			fd.Show()
		}

		folderUploadFunc := func() {
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

		// 创建带图标的按钮，使界面更美观
		fileBtn := widget.NewButtonWithIcon("上传文件", theme.FileIcon(), fileUploadFunc)
		folderBtn := widget.NewButtonWithIcon("上传文件夹", theme.FolderIcon(), folderUploadFunc)

		// 设置按钮大小和样式
		fileBtn.Importance = widget.HighImportance
		folderBtn.Importance = widget.HighImportance

		// 创建垂直布局的内容，增加间距
		content := container.NewVBox(
			container.NewCenter(widget.NewLabelWithStyle("请选择上传类型", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})),
			widget.NewSeparator(),
			container.NewPadded(fileBtn),
			container.NewPadded(folderBtn),
		)

		// 创建自定义对话框并设置合适的尺寸
		uploadDialog := dialog.NewCustom("上传文件", "取消", content, ov.window)
		uploadDialog.Resize(fyne.NewSize(300, 200)) // 调整高度
		uploadDialog.Show()
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalUploadButtonOnTapped := uploadButton.OnTapped
		uploadButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(uploadButton, func() {
				if originalUploadButtonOnTapped != nil {
					originalUploadButtonOnTapped()
				}
			})
		}
	}

	ov.downloadButton = widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
		// 动画结束后执行的逻辑
		if len(ov.selectedObjectIDs) == 0 {
			ShowToast(ov.window, "请至少选择一个要下载的项目。")
			return
		}

		// 使用系统文件管理器选择下载目录
		go ov.openSystemFolderSelector()
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalDownloadButtonOnTapped := ov.downloadButton.OnTapped
		ov.downloadButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(ov.downloadButton, func() {
				if originalDownloadButtonOnTapped != nil {
					originalDownloadButtonOnTapped()
				}
			})
		}
	}
	ov.deleteButton = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		// 动画结束后执行的逻辑
		selectedCount := len(ov.selectedObjectIDs)
		if selectedCount == 0 {
			ShowToast(ov.window, "请先选择要删除的文件或文件夹。")
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
							ShowToast(ov.window, fmt.Sprintf("%d 个项目已成功删除。", selectedCount))
						}
						ov.resetPagingAndSelection()
						ov.loadObjects()
					})
				}()
			}
		}, ov.window)
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalDeleteButtonOnTapped := ov.deleteButton.OnTapped
		ov.deleteButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(ov.deleteButton, func() {
				if originalDeleteButtonOnTapped != nil {
					originalDeleteButtonOnTapped()
				}
			})
		}
	}
	ov.updateButtonsState()

	ov.viewSwitchButton = widget.NewButtonWithIcon("", theme.GridIcon(), func() {
		// 动画结束后执行的逻辑
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

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalViewSwitchButtonOnTapped := ov.viewSwitchButton.OnTapped
		ov.viewSwitchButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(ov.viewSwitchButton, func() {
				if originalViewSwitchButtonOnTapped != nil {
					originalViewSwitchButtonOnTapped()
				}
			})
		}
	}

	fileOpsButtons := container.NewHBox(createFolderButton, uploadButton, ov.downloadButton, ov.deleteButton, ov.viewSwitchButton)

	topBar := container.NewBorder(nil, nil, ov.breadcrumbContainer, fileOpsButtons, ov.searchEntry)

	// 将顶部栏、加载指示器和分隔符组合在一起
	topContent := container.NewVBox(topBar, ov.loadingIndicator, widget.NewSeparator())

	// --- 分页控件 ---
	ov.prevButton = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		// 动画结束后执行的逻辑
		if ov.currentPage > 1 {
			ov.currentPage--
			ov.loadObjects()
		}
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalPrevButtonOnTapped := ov.prevButton.OnTapped
		ov.prevButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(ov.prevButton, func() {
				if originalPrevButtonOnTapped != nil {
					originalPrevButtonOnTapped()
				}
			})
		}
	}

	ov.nextButton = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		// 动画结束后执行的逻辑
		if ov.nextPageMarker != nil {
			ov.currentPage++
			ov.loadObjects()
		}
	})

	// 为按钮添加点击动画
	if ov.animationManager != nil {
		originalNextButtonOnTapped := ov.nextButton.OnTapped
		ov.nextButton.OnTapped = func() {
			ov.animationManager.AnimateButtonClick(ov.nextButton, func() {
				if originalNextButtonOnTapped != nil {
					originalNextButtonOnTapped()
				}
			})
		}
	}
	ov.pageInfoLabel = widget.NewLabel("")
	ov.pageSizeEntry = newMinWidthEntry(80)
	ov.pageSizeEntry.SetText(strconv.Itoa(ov.pageSize))
	ov.pageSizeEntry.OnSubmitted = func(s string) {
		ps, err := strconv.Atoi(s)
		if err != nil || ps < 0 {
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

	// 主布局，顶部是组合控件，中间是主内容
	return container.NewBorder(topContent, statusBar, nil, nil, ov.mainContent)
}

// findAvailableObjectKey 检查目标key是否存在，如果存在，则返回一个带递增数字的新key。
func (ov *ObjectsView) findAvailableObjectKey(s3Key string) (string, error) {
	// 1. Check if original key is available
	exists, err := ov.s3Client.ObjectExists(ov.currentBucket, s3Key)
	if err != nil {
		return "", fmt.Errorf("检查对象 '%s' 是否存在时出错: %w", s3Key, err)
	}
	if !exists {
		return s3Key, nil
	}

	// 2. If it exists, try with (n)
	ext := filepath.Ext(s3Key)
	keyWithoutExt := strings.TrimSuffix(s3Key, ext)

	for i := 1; ; i++ {
		newKey := fmt.Sprintf("%s(%d)%s", keyWithoutExt, i, ext)
		exists, err := ov.s3Client.ObjectExists(ov.currentBucket, newKey)
		if err != nil {
			return "", fmt.Errorf("检查对象 '%s' 是否存在时出错: %w", newKey, err)
		}
		if !exists {
			return newKey, nil
		}
	}
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

				availableFolderName, err := ov.findAvailableFolderName(baseFolderName)
				if err != nil {
					scanMu.Lock()
					scanErrors = append(scanErrors, fmt.Errorf("查找可用文件夹名称失败 '%s': %w", baseFolderName, err))
					scanMu.Unlock()
					return
				}

				err = filepath.Walk(path, func(p string, i os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					relPath, err := filepath.Rel(path, p)
					if err != nil {
						return err
					}
					s3Key := filepath.Join(ov.currentPrefix, availableFolderName, relPath)
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

				availableKey, err := ov.findAvailableObjectKey(s3Key)
				if err != nil {
					scanMu.Lock()
					scanErrors = append(scanErrors, fmt.Errorf("查找可用对象key失败 '%s': %w", s3Key, err))
					scanMu.Unlock()
					return
				}

				scanMu.Lock()
				filesToUpload = append(filesToUpload, struct {
					LocalPath string
					S3Key     string
					Size      int64
				}{LocalPath: path, S3Key: availableKey, Size: info.Size()})
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
			ShowToast(ov.window, "没有可上传的项目。")
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
			ShowToast(ov.window, "没有可下载的项目。")
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
			ShowToast(ov.window, "所有项目下载完成。")
		}
		ov.loadObjects()
	})
}

// openSystemFolderSelector 打开系统文件管理器让用户选择下载目录
func (ov *ObjectsView) openSystemFolderSelector() {
	// 使用系统对话框让用户选择下载目录
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, ov.window)
			return
		}
		if uri == nil {
			return
		}
		// 开始下载过程
		go ov.startDownloadProcess(uri.Path())
	}, ov.window)
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

// downloadCopiedObjects 下载复制的S3对象到本地目录
func (ov *ObjectsView) downloadCopiedObjects(localBasePath string, objectsToDownload []s3client.S3Object) {
	scanProgressDialog := dialog.NewProgressInfinite("正在准备下载", "正在计算下载大小...", ov.window)
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
	numScanWorkers := 5 // 根据需要进行调整
	objectChannel := make(chan s3client.S3Object, len(objectsToDownload))

	for i := 0; i < numScanWorkers; i++ {
		scanWg.Add(1)
		go func() {
			defer scanWg.Done()
			for obj := range objectChannel {
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

	for _, obj := range objectsToDownload {
		objectChannel <- obj
	}
	close(objectChannel)
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
			ShowToast(ov.window, "没有可下载的项目。")
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
			ShowToast(ov.window, "所有项目已下载完成。")
		}
	})
}

// pasteS3Objects 在S3存储桶内复制对象
func (ov *ObjectsView) pasteS3Objects(objectsToCopy []s3client.S3Object) {
	if ov.s3Client == nil || ov.currentBucket == "" {
		dialog.ShowError(fmt.Errorf("未选择S3服务或存储桶"), ov.window)
		return
	}

	// 显示进度对话框
	progressDialog := dialog.NewProgressInfinite("正在复制", "正在复制对象...", ov.window)
	progressDialog.Show()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error
	var successCount int

	// 为每个对象启动一个goroutine进行复制
	for _, obj := range objectsToCopy {
		wg.Add(1)
		go func(object s3client.S3Object) {
			defer wg.Done()

			if object.IsFolder {
				// 处理文件夹复制
				err := ov.copyFolderRecursive(object)
				if err != nil {
					mu.Lock()
					errors = append(errors, fmt.Errorf("复制文件夹 '%s' 时出错: %v", object.Name, err))
					mu.Unlock()
				} else {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			} else {
				// 处理文件复制
				err := ov.copySingleObject(object)
				if err != nil {
					mu.Lock()
					errors = append(errors, fmt.Errorf("复制文件 '%s' 时出错: %v", object.Name, err))
					mu.Unlock()
				} else {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}
		}(obj)
	}

	// 等待所有复制操作完成
	wg.Wait()

	// 关闭进度对话框
	fyne.Do(func() {
		progressDialog.Hide()

		// 显示结果
		mu.Lock()
		errorCount := len(errors)
		mu.Unlock()

		if errorCount > 0 {
			// 收集错误信息
			errorMessages := make([]string, len(errors))
			for i, err := range errors {
				errorMessages[i] = err.Error()
			}
			dialog.ShowError(fmt.Errorf("部分对象复制失败 (%d/%d):\n%s", errorCount, len(objectsToCopy), strings.Join(errorMessages, "\n")), ov.window)
		} else {
			ShowToast(ov.window, fmt.Sprintf("成功复制 %d 个对象。", successCount))
		}

		// 刷新对象列表
		ov.loadObjects()
	})
}

// copySingleObject 复制单个文件对象
func (ov *ObjectsView) copySingleObject(object s3client.S3Object) error {
	// 生成目标对象键（在当前目录下）
	originalName := object.Name
	targetKey := ov.currentPrefix + originalName

	log.Printf("准备复制文件: %s -> %s", object.Key, targetKey)

	// 检查目标对象是否已存在，如果存在则生成新名称
	newKey := targetKey
	counter := 1
	for {
		// 检查对象是否存在
		exists, err := ov.s3Client.ObjectExists(ov.currentBucket, newKey)
		if err != nil {
			return fmt.Errorf("检查对象 '%s' 是否存在时出错: %v", newKey, err)
		}

		if !exists {
			break // 对象不存在，可以使用这个名称
		}

		// 对象已存在，生成新名称
		ext := filepath.Ext(originalName)
		nameWithoutExt := strings.TrimSuffix(originalName, ext)
		newKey = ov.currentPrefix + fmt.Sprintf("%s(%d)%s", nameWithoutExt, counter, ext)
		counter++

		log.Printf("对象已存在，尝试新名称: %s", newKey)
	}

	// 执行复制操作
	err := ov.s3Client.CopyObject(ov.currentBucket, object.Key, newKey)
	if err != nil {
		return fmt.Errorf("复制对象 '%s' 到 '%s' 时出错: %v", object.Key, newKey, err)
	}

	log.Printf("成功复制文件: %s -> %s", object.Key, newKey)
	return nil
}

// findAvailableFolderName 检查目标前缀中是否存在同名文件夹，如果存在，则返回一个带递增数字的新名称。
func (ov *ObjectsView) findAvailableFolderName(baseName string) (string, error) {
	// 1. 检查原始名称是否可用
	destKeyPrefix := ov.currentPrefix + baseName + "/"

	// 使用 ListAllObjectsUnderPrefix 检查文件夹下是否有内容
	objects, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, destKeyPrefix)
	if err != nil {
		// 假设任何列出错误都意味着我们无法安全地确定存在性
		return "", fmt.Errorf("检查文件夹 '%s' 是否存在时出错: %w", destKeyPrefix, err)
	}

	// 即使文件夹为空，它也可能作为一个0字节的对象存在
	folderObjectExists, err := ov.s3Client.ObjectExists(ov.currentBucket, destKeyPrefix)
	if err != nil {
		return "", fmt.Errorf("检查文件夹对象 '%s' 是否存在时出错: %w", destKeyPrefix, err)
	}

	// 如果文件夹内没有对象并且文件夹本身的对象也不存在，则该名称可用
	if len(objects) == 0 && !folderObjectExists {
		return baseName, nil
	}

	// 2. 如果原始名称不可用，尝试 "baseName(n)"
	for i := 1; ; i++ {
		newName := fmt.Sprintf("%s(%d)", baseName, i)
		destKeyPrefix = ov.currentPrefix + newName + "/"

		objects, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, destKeyPrefix)
		if err != nil {
			return "", fmt.Errorf("检查文件夹 '%s' 是否存在时出错: %w", destKeyPrefix, err)
		}

		folderObjectExists, err := ov.s3Client.ObjectExists(ov.currentBucket, destKeyPrefix)
		if err != nil {
			return "", fmt.Errorf("检查文件夹对象 '%s' 是否存在时出错: %w", destKeyPrefix, err)
		}

		if len(objects) == 0 && !folderObjectExists {
			return newName, nil
		}
	}
}

// copyFolderRecursive 递归复制文件夹及其所有内容
func (ov *ObjectsView) copyFolderRecursive(folder s3client.S3Object) error {
	originalFolderName := strings.TrimSuffix(folder.Name, "/")

	// 查找可用的文件夹名称
	availableName, err := ov.findAvailableFolderName(originalFolderName)
	if err != nil {
		return fmt.Errorf("查找可用文件夹名称失败 for '%s': %w", originalFolderName, err)
	}

	newFolderKey := ov.currentPrefix + availableName + "/"
	log.Printf("准备复制文件夹: %s -> %s", folder.Key, newFolderKey)

	// 列出源文件夹中的所有对象
	objects, err := ov.s3Client.ListAllObjectsUnderPrefix(ov.currentBucket, folder.Key)
	if err != nil {
		return fmt.Errorf("列出源文件夹 '%s' 内容时出错: %v", folder.Key, err)
	}

	// 复制每个对象到目标文件夹
	for _, obj := range objects {
		// 计算目标对象键
		relativePath := strings.TrimPrefix(obj.Key, folder.Key)
		targetKey := newFolderKey + relativePath

		// 因为目标文件夹是全新的，所以我们直接复制，不检查是否存在。
		// 这会保留源文件夹的结构。
		err := ov.s3Client.CopyObject(ov.currentBucket, obj.Key, targetKey)
		if err != nil {
			// 如果单个对象复制失败，记录并继续尝试复制其他对象
			log.Printf("复制对象 '%s' 到 '%s' 时出错: %v", obj.Key, targetKey, err)
		} else {
			log.Printf("成功复制对象: %s -> %s", obj.Key, targetKey)
		}
	}

	log.Printf("成功复制文件夹: %s -> %s", folder.Key, newFolderKey)
	return nil
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

		// 对过滤后的对象进行排序，确保文件夹在前
		sort.Slice(ov.filteredObjects, func(i, j int) bool {
			// 如果一个是文件夹，另一个是文件，则文件夹排在前面
			if ov.filteredObjects[i].IsFolder && !ov.filteredObjects[j].IsFolder {
				return true
			}
			if !ov.filteredObjects[i].IsFolder && ov.filteredObjects[j].IsFolder {
				return false
			}
			// 如果两个都是文件夹或都是文件，则按名称排序
			return ov.filteredObjects[i].Name < ov.filteredObjects[j].Name
		})
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
