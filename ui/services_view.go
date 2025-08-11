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
	editButton        *widget.Button
	deleteButton      *widget.Button

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
			// 可以在这里显示一个错误对话框，但由于这是后台操作，日志可能更合适
		} else {
			// 成功更新后，重新加载配置以确保 UI 同步
			sv.loadConfig()
		}
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
				err := sv.configStore.AddService(newService)
				if err != nil {
					dialog.ShowError(fmt.Errorf("添加服务失败: %v", err), sv.window)
					return
				}
				sv.loadConfig()
				sv.refreshServiceList()
				// 添加后，如果选择了服务，请重新选择它以更新右侧面板
				if sv.selectedServiceID != -1 && sv.OnServiceSelected != nil {
					// 如果新添加的服务是第一个，确保选择它
					if len(sv.configStore.Services) == 1 {
						sv.selectedServiceID = 0
					}
					sv.OnServiceSelected(sv.configStore.Services[sv.selectedServiceID])
				}
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
		oldAlias := selectedService.Alias // Capture old alias before newService is created
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
				err := sv.configStore.UpdateService(oldAlias, newService)
				if err != nil {
					dialog.ShowError(fmt.Errorf("更新服务失败: %v", err), sv.window)
					return
				}
				sv.loadConfig()
				sv.refreshServiceList()

				// 找到更新后的服务的新索引并重新选择它
				newlySelectedID := -1
				for i, svc := range sv.configStore.Services {
					if svc.Alias == newService.Alias { // Find by new alias
						newlySelectedID = i
						break
					}
				}

				if newlySelectedID != -1 && sv.OnServiceSelected != nil {
					sv.selectedServiceID = newlySelectedID // Update selected ID
					sv.OnServiceSelected(sv.configStore.Services[sv.selectedServiceID])
				} else {
					// If for some reason the service is not found after update (shouldn't happen),
					// or if no service was selected before, clear the right panel.
					sv.selectedServiceID = -1
					if sv.OnServiceSelected != nil {
						sv.OnServiceSelected(config.S3ServiceConfig{})
					}
				}
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
				sv.loadConfig()
				sv.refreshServiceList()
				// 删除后如果仍有服务并且选择了服务，
				// 尝试重新选择第一个服务或清除右侧面板。
				if len(sv.configStore.Services) > 0 {
					sv.selectedServiceID = 0 // 选择第一项服务
					if sv.OnServiceSelected != nil {
						sv.OnServiceSelected(sv.configStore.Services[sv.selectedServiceID])
					}
				} else {
					sv.selectedServiceID = -1 // 没有剩余服务
					if sv.OnServiceSelected != nil {
						sv.OnServiceSelected(config.S3ServiceConfig{})
					}
				}
			}
		}, sv.window)
	})

	sv.updateButtonsState() // 设置按钮初始状态

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
