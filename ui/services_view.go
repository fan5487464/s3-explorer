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
	serviceList       *widget.List
	selectedServiceID widget.ListItemID
	loadingIndicator  *widget.ProgressBarInfinite
	editButton        *widget.Button
	deleteButton      *widget.Button

	OnServiceSelected func(svc config.S3ServiceConfig)
}

// NewServicesView 创建并返回一个新的 ServicesView 实例
func NewServicesView(w fyne.Window) *ServicesView {
	sv := &ServicesView{
		window:            w,
		selectedServiceID: -1,
		loadingIndicator:  widget.NewProgressBarInfinite(),
	}
	sv.loadingIndicator.Hide()
	sv.loadConfig(nil)
	return sv
}

// UpdateServiceViewMode 更新内存中服务的视图模式并保存到文件
func (sv *ServicesView) UpdateServiceViewMode(alias string, viewMode string) {
	if sv.configStore == nil {
		return
	}

	var serviceToUpdate config.S3ServiceConfig
	found := false
	for _, s := range sv.configStore.Services {
		if s.Alias == alias {
			serviceToUpdate = s
			found = true
			break
		}
	}

	if found {
		serviceToUpdate.ViewMode = viewMode
		err := sv.configStore.UpdateService(alias, serviceToUpdate)
		if err != nil {
			log.Printf("更新服务 '%s' 的视图模式失败: %v", alias, err)
		} else {
			sv.loadConfig(nil)
		}
	} else {
		log.Printf("无法找到服务 '%s' 来更新视图模式。", alias)
	}
}

func (sv *ServicesView) handleServiceTapped(id widget.ListItemID) {
	if sv.selectedServiceID == id {
		sv.selectedServiceID = -1
		if sv.OnServiceSelected != nil {
			sv.OnServiceSelected(config.S3ServiceConfig{})
		}
	} else {
		sv.selectedServiceID = id
		if sv.OnServiceSelected != nil {
			if sv.configStore != nil && id >= 0 && id < len(sv.configStore.Services) {
				sv.OnServiceSelected(sv.configStore.Services[id])
			}
		}
	}
	sv.serviceList.Refresh()
	sv.updateButtonsState()
}

// updateButtonsState 根据选择状态更新按钮可用性
func (sv *ServicesView) updateButtonsState() {
	if sv.editButton == nil || sv.deleteButton == nil {
		return
	}
	if sv.selectedServiceID == -1 {
		sv.editButton.Disable()
		sv.deleteButton.Disable()
	} else {
		sv.editButton.Enable()
		sv.deleteButton.Enable()
	}
}

// loadConfig 加载 S3 服务配置，并在完成后执行回调
func (sv *ServicesView) loadConfig(onComplete func()) {
	sv.loadingIndicator.Show()
	go func() {
		store, err := config.LoadConfig()
		fyne.Do(func() {
			sv.loadingIndicator.Hide()
			if err != nil {
				log.Printf("加载配置失败: %v", err)
				sv.configStore = &config.ConfigStore{Services: []config.S3ServiceConfig{}}
				dialog.ShowError(fmt.Errorf("加载配置失败: %v", err), sv.window)
			} else {
				sv.configStore = store
			}
			sv.refreshServiceList()
			if onComplete != nil {
				onComplete()
			}
		})
	}()
}

// refreshServiceList 刷新服务列表显示
func (sv *ServicesView) refreshServiceList() {
	if sv.serviceList == nil {
		return
	}
	sv.serviceList.Refresh()
}

// createServiceFormContent 创建一个用于添加/编辑服务配置的表单内容
func (sv *ServicesView) createServiceFormContent(service *config.S3ServiceConfig) (fyne.CanvasObject, *widget.Entry, *widget.Entry, *widget.Entry, *widget.Entry, *widget.Entry) {
	aliasEntry := widget.NewEntry()
	aliasEntry.SetPlaceHolder("例如：我的Minio")
	endpointEntry := widget.NewEntry()
	endpointEntry.SetPlaceHolder("例如：http://localhost:9000")
	accessKeyEntry := widget.NewEntry()
	secretKeyEntry := widget.NewPasswordEntry()
	proxyEntry := widget.NewEntry()
	proxyEntry.SetPlaceHolder("例如：http://127.0.0.1:7890")

	if service != nil {
		aliasEntry.SetText(service.Alias)
		endpointEntry.SetText(service.Endpoint)
		accessKeyEntry.SetText(service.AccessKey)
		secretKeyEntry.SetText(service.SecretKey)
		proxyEntry.SetText(service.Proxy)
	}

	formContent := container.New(layout.NewFormLayout(),
		widget.NewLabel("别名:"), aliasEntry,
		widget.NewLabel("Endpoint:"), endpointEntry,
		widget.NewLabel("Access Key:"), accessKeyEntry,
		widget.NewLabel("Secret Key:"), secretKeyEntry,
		widget.NewLabel("Proxy:"), proxyEntry,
	)
	return formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry, proxyEntry
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
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry, proxyEntry := sv.createServiceFormContent(nil)
		d := dialog.NewCustomConfirm("添加 S3 服务", "添加", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
					Proxy:     proxyEntry.Text,
				}
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "除了代理，所有字段都不能为空！", sv.window)
					return
				}
				err := sv.configStore.AddService(newService)
				if err != nil {
					dialog.ShowError(fmt.Errorf("添加服务失败: %v", err), sv.window)
					return
				}
				sv.loadConfig(func() {
					// 添加后，自动选择新添加的服务
					newlySelectedID := -1
					for i, svc := range sv.configStore.Services {
						if svc.Alias == newService.Alias {
							newlySelectedID = i
							break
						}
					}
					if newlySelectedID != -1 {
						sv.handleServiceTapped(newlySelectedID)
					}
				})
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250))
		d.Show()
	})

	// 编辑服务按钮
	sv.editButton = widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		if sv.selectedServiceID == -1 || sv.selectedServiceID >= len(sv.configStore.Services) {
			dialog.ShowInformation("提示", "请先选择一个要编辑的服务。", sv.window)
			return
		}
		selectedService := sv.configStore.Services[sv.selectedServiceID]
		oldAlias := selectedService.Alias
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry, proxyEntry := sv.createServiceFormContent(&selectedService)
		d := dialog.NewCustomConfirm("编辑 S3 服务", "保存", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
					ViewMode:  selectedService.ViewMode,
					Proxy:     proxyEntry.Text,
				}
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "除了代理，所有字段都不能为空！", sv.window)
					return
				}
				err := sv.configStore.UpdateService(oldAlias, newService)
				if err != nil {
					dialog.ShowError(fmt.Errorf("更新服务失败: %v", err), sv.window)
					return
				}
				sv.loadConfig(func() {
					// 找到更新后的服务的新索引并重新选择它
					newlySelectedID := -1
					for i, svc := range sv.configStore.Services {
						if svc.Alias == newService.Alias {
							newlySelectedID = i
							break
						}
					}

					if newlySelectedID != -1 {
						sv.handleServiceTapped(newlySelectedID)
					} else {
						sv.handleServiceTapped(-1) // Clear selection if not found
					}
				})
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250))
		d.Show()
	})

	// 删除服务按钮
	sv.deleteButton = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		if sv.selectedServiceID == -1 || sv.selectedServiceID >= len(sv.configStore.Services) {
			dialog.ShowInformation("提示", "请先选择一个要删除的服务。", sv.window)
			return
		}
		selectedService := sv.configStore.Services[sv.selectedServiceID]

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除服务 \"%s\" 吗？", selectedService.Alias), func(confirmed bool) {
			if confirmed {
				err := sv.configStore.DeleteService(selectedService.Alias)
				if err != nil {
					dialog.ShowError(fmt.Errorf("删除服务失败: %v", err), sv.window)
					return
				}
				sv.loadConfig(func() {
					// 删除后，清除选择或选择第一个
					if len(sv.configStore.Services) > 0 {
						sv.handleServiceTapped(0)
					} else {
						sv.handleServiceTapped(-1)
					}
				})
			}
		}, sv.window)
	})

	sv.updateButtonsState()

	buttonBox := container.NewHBox(
		addButton,
		layout.NewSpacer(),
		sv.editButton,
		layout.NewSpacer(),
		sv.deleteButton,
		layout.NewSpacer(),
		sv.loadingIndicator,
	)

	return container.NewBorder(buttonBox, nil, nil, nil, container.NewVBox(widget.NewSeparator()), sv.serviceList)
}