package ui

import (
	"fmt"
	"image/color"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"s3-explorer/config" // 导入我们之前创建的 config 包
)

// serviceListEntry 是服务列表的自定义列表项
type serviceListEntry struct {
	widget.BaseWidget
	label    *widget.Label
	id       widget.ListItemID
	sv       *ServicesView
	selected bool
}

func (e *serviceListEntry) Tapped(_ *fyne.PointEvent) {
	e.sv.handleServiceTapped(e.id)
}

func (e *serviceListEntry) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	return &serviceListEntryRenderer{
		entry:      e,
		background: bg,
		content:    container.NewStack(bg, e.label),
	}
}

// serviceListEntryRenderer 自定义渲染器
type serviceListEntryRenderer struct {
	entry      *serviceListEntry
	background *canvas.Rectangle
	content    *fyne.Container
}

func (r *serviceListEntryRenderer) Destroy() {}
func (r *serviceListEntryRenderer) Layout(s fyne.Size) {
	r.content.Resize(s)
}
func (r *serviceListEntryRenderer) MinSize() fyne.Size {
	return r.content.MinSize()
}
func (r *serviceListEntryRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.content}
}
func (r *serviceListEntryRenderer) Refresh() {
	if r.entry.selected {
		r.background.FillColor = theme.SelectionColor()
	} else {
		r.background.FillColor = color.Transparent
	}
	r.background.Refresh()
}

// ServicesView 结构体用于管理左侧的服务列表视图
type ServicesView struct {
	window            fyne.Window
	configStore       *config.ConfigStore
	serviceList       *widget.List                // 用于显示 S3 服务列表的 Fyne 列表组件
	selectedServiceID widget.ListItemID           // 存储当前选中的服务 ID
	loadingIndicator  *widget.ProgressBarInfinite // 加载指示器

	OnServiceSelected func(svc config.S3ServiceConfig)
}

// NewServicesView 创建并返回一个新的 ServicesView 实例
func NewServicesView(w fyne.Window) *ServicesView {
	sv := &ServicesView{
		window:            w,
		selectedServiceID: -1,                              // 初始状态为未选中
		loadingIndicator:  widget.NewProgressBarInfinite(), // 初始化加载指示器
	}
	sv.loadingIndicator.Hide() // 默认隐藏
	sv.loadConfig()            // 加载配置
	return sv
}

// UpdateServiceViewMode 更新内存中服务的视图模式并保存到文件
func (sv *ServicesView) UpdateServiceViewMode(alias string, viewMode string) {
	if sv.configStore == nil {
		return
	}
	found := false
	for i, s := range sv.configStore.Services {
		if s.Alias == alias {
			sv.configStore.Services[i].ViewMode = viewMode
			found = true
			break
		}
	}

	if found {
		sv.saveConfig()
	} else {
		log.Printf("无法找到服务 '%s' 来更新视图模式。", alias)
	}
}

func (sv *ServicesView) handleServiceTapped(id widget.ListItemID) {
	// 如果点击的是已选中的项，则取消选择
	if sv.selectedServiceID == id {
		sv.selectedServiceID = -1
		if sv.OnServiceSelected != nil {
			// 传递一个空的服务配置来清空后续视图
			sv.OnServiceSelected(config.S3ServiceConfig{})
		}
	} else {
		// 否则，选中新点击的项
		sv.selectedServiceID = id // 记录选中的 ID
		if sv.OnServiceSelected != nil {
			if sv.configStore != nil && id >= 0 && id < len(sv.configStore.Services) {
				sv.OnServiceSelected(sv.configStore.Services[id])
			}
		}
	}
	sv.serviceList.Refresh() // 刷新列表以更新视觉效果
}

// loadConfig 加载 S3 服务配置
func (sv *ServicesView) loadConfig() {
	sv.loadingIndicator.Show() // 显示加载指示器
	go func() {
		store, err := config.LoadConfig()
		fyne.Do(func() {
			sv.loadingIndicator.Hide() // 隐藏加载指示器
			if err != nil {
				log.Printf("加载配置失败: %v", err)
				sv.configStore = &config.ConfigStore{Services: []config.S3ServiceConfig{}}
				dialog.ShowError(fmt.Errorf("加载配置失败: %v", err), sv.window)
				return
			}
			sv.configStore = store
			sv.refreshServiceList()
		})
	}()
}

// saveConfig 保存 S3 服务配置
func (sv *ServicesView) saveConfig() {
	err := config.SaveConfig(sv.configStore)
	if err != nil {
		dialog.ShowError(fmt.Errorf("保存配置失败: %v", err), sv.window)
	}
}

// refreshServiceList 刷新服务列表显示
func (sv *ServicesView) refreshServiceList() {
	if sv.serviceList == nil {
		return
	}
	sv.serviceList.Refresh()
}

// createServiceFormContent 创建一个用于添加/编辑服务配置的表单内容
func (sv *ServicesView) createServiceFormContent(service *config.S3ServiceConfig) (fyne.CanvasObject, *widget.Entry, *widget.Entry, *widget.Entry, *widget.Entry) {
	aliasEntry := widget.NewEntry()
	aliasEntry.SetPlaceHolder("例如：我的Minio")
	endpointEntry := widget.NewEntry()
	endpointEntry.SetPlaceHolder("例如：http://localhost:9000")
	accessKeyEntry := widget.NewEntry()
	secretKeyEntry := widget.NewPasswordEntry()

	if service != nil {
		aliasEntry.SetText(service.Alias)
		endpointEntry.SetText(service.Endpoint)
		accessKeyEntry.SetText(service.AccessKey)
		secretKeyEntry.SetText(service.SecretKey)
	}

	formContent := container.New(layout.NewFormLayout(),
		widget.NewLabel("别名:"), aliasEntry,
		widget.NewLabel("Endpoint:"), endpointEntry,
		widget.NewLabel("Access Key:"), accessKeyEntry,
		widget.NewLabel("Secret Key:"), secretKeyEntry,
	)
	return formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry
}

// GetContent 返回 ServicesView 的 Fyne UI 内容
func (sv *ServicesView) GetContent() fyne.CanvasObject {
	sv.serviceList = widget.NewList(
		func() int {
			if sv.configStore == nil {
				return 0
			}
			return len(sv.configStore.Services)
		},
		func() fyne.CanvasObject {
			entry := &serviceListEntry{
				label: widget.NewLabel("服务别名"),
				sv:    sv,
			}
			entry.ExtendBaseWidget(entry)
			return entry
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			entry := obj.(*serviceListEntry)
			entry.id = id
			entry.label.SetText(sv.configStore.Services[id].Alias)
			entry.selected = sv.selectedServiceID == id
			entry.Refresh()
		},
	)

	// 添加服务按钮
	addButton := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry := sv.createServiceFormContent(nil)
		d := dialog.NewCustomConfirm("添加 S3 服务", "添加", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
				}
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "所有字段都不能为空！", sv.window)
					return
				}
				sv.configStore.AddService(newService)
				sv.saveConfig()
				sv.refreshServiceList()
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250))
		d.Show()
	})

	// 编辑服务按钮
	editButton := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		if sv.selectedServiceID == -1 || sv.selectedServiceID >= len(sv.configStore.Services) {
			dialog.ShowInformation("提示", "请先选择一个要编辑的服务。", sv.window)
			return
		}
		selectedService := sv.configStore.Services[sv.selectedServiceID]
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry := sv.createServiceFormContent(&selectedService)
		d := dialog.NewCustomConfirm("编辑 S3 服务", "保存", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
					ViewMode:  selectedService.ViewMode, // 保留旧的视图模式
				}
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "所有字段都不能为空！", sv.window)
					return
				}
				sv.configStore.UpdateService(selectedService.Alias, newService)
				sv.saveConfig()
				sv.refreshServiceList()
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250))
		d.Show()
	})

	// 删除服务按钮
	deleteButton := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		if sv.selectedServiceID == -1 || sv.selectedServiceID >= len(sv.configStore.Services) {
			dialog.ShowInformation("提示", "请先选择一个要删除的服务。", sv.window)
			return
		}
		selectedService := sv.configStore.Services[sv.selectedServiceID]

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除服务 \"%s\" 吗？", selectedService.Alias), func(confirmed bool) {
			if confirmed {
				sv.configStore.DeleteService(selectedService.Alias)
				sv.saveConfig()
				sv.refreshServiceList()
				sv.selectedServiceID = -1 // 重置选中 ID
				if sv.OnServiceSelected != nil {
					sv.OnServiceSelected(config.S3ServiceConfig{})
				}
			}
		}, sv.window)
	})

	buttonBox := container.NewHBox(
		addButton,
		layout.NewSpacer(),
		editButton,
		layout.NewSpacer(),
		deleteButton,
		layout.NewSpacer(),
		sv.loadingIndicator,
	)

	return container.NewBorder(buttonBox, nil, nil, nil, container.NewVBox(widget.NewSeparator()), sv.serviceList)
}