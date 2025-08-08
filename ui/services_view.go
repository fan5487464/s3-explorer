package ui

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"s3-explorer/config" // 导入我们之前创建的 config 包
)

// ServicesView 结构体用于管理左侧的服务列表视图
type ServicesView struct {
	window          fyne.Window
	configStore     *config.ConfigStore
	serviceList *widget.List // 用于显示 S3 服务列表的 Fyne 列表组件
	selectedServiceID widget.ListItemID // 存储当前选中的服务 ID

	// onServiceSelected 是一个回调函数，当用户选择一个服务时触发
	// 参数是选中的服务配置
	OnServiceSelected func(svc config.S3ServiceConfig)
}

// NewServicesView 创建并返回一个新的 ServicesView 实例
func NewServicesView(w fyne.Window) *ServicesView {
	sv := &ServicesView{
		window:          w,
		selectedServiceID: -1, // 初始状态为未选中
	}
	sv.loadConfig() // 加载配置
	return sv
}

// loadConfig 加载 S3 服务配置
func (sv *ServicesView) loadConfig() {
	store, err := config.LoadConfig()
	if err != nil {
		log.Printf("加载配置失败: %v", err)
		// 如果加载失败，初始化一个空的配置存储
		sv.configStore = &config.ConfigStore{Services: []config.S3ServiceConfig{}}
		return
	}
	sv.configStore = store
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
	// 如果 serviceList 还没有初始化，则不执行刷新
	if sv.serviceList == nil {
		return
	}
	sv.serviceList.Refresh()
}

// createServiceFormContent 创建一个用于添加/编辑服务配置的表单内容
// 返回表单的 Fyne UI 内容和各个输入框的引用
func (sv *ServicesView) createServiceFormContent(service *config.S3ServiceConfig) (fyne.CanvasObject, *widget.Entry, *widget.Entry, *widget.Entry, *widget.Entry) {
	aliasEntry := widget.NewEntry()
	aliasEntry.SetPlaceHolder("例如：我的Minio")
	endpointEntry := widget.NewEntry()
	endpointEntry.SetPlaceHolder("例如：http://localhost:9000")
	accessKeyEntry := widget.NewEntry()
	secretKeyEntry := widget.NewPasswordEntry() // 密码输入框

	// 如果是编辑模式，填充现有数据
	if service != nil {
		aliasEntry.SetText(service.Alias)
		endpointEntry.SetText(service.Endpoint)
		accessKeyEntry.SetText(service.AccessKey)
		secretKeyEntry.SetText(service.SecretKey)
	}

	// 使用 layout.NewFormLayout() 创建响应式表单布局
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
			return len(sv.configStore.Services)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("服务别名") // 列表项的模板
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// 更新列表项内容
			label := obj.(*widget.Label)
			label.SetText(sv.configStore.Services[id].Alias)
		},
	)

	// 设置列表项点击事件 (左键)
	sv.serviceList.OnSelected = func(id widget.ListItemID) {
		sv.selectedServiceID = id // 记录选中的 ID
		if sv.OnServiceSelected != nil {
			sv.OnServiceSelected(sv.configStore.Services[id])
		}
	}

	// 添加服务按钮 (只显示图标)
	addButton := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry := sv.createServiceFormContent(nil) // nil 表示添加新服务
		d := dialog.NewCustomConfirm("添加 S3 服务", "添加", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
				}
				// 验证输入
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "所有字段都不能为空！", sv.window)
					return // 不保存，并保持对话框打开
				}

				sv.configStore.AddService(newService)
				sv.saveConfig()
				sv.refreshServiceList()
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250)) // 设置对话框的最小尺寸
		d.Show()
	})

	// 编辑服务按钮 (只显示图标)
	editButton := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		if sv.selectedServiceID == -1 || sv.selectedServiceID >= len(sv.configStore.Services) {
			dialog.ShowInformation("提示", "请先选择一个要编辑的服务。", sv.window)
			return
		}
		selectedService := sv.configStore.Services[sv.selectedServiceID]
		formContent, aliasEntry, endpointEntry, accessKeyEntry, secretKeyEntry := sv.createServiceFormContent(&selectedService) // 传入选中的服务进行编辑
		d := dialog.NewCustomConfirm("编辑 S3 服务", "保存", "取消", formContent, func(confirmed bool) {
			if confirmed {
				newService := config.S3ServiceConfig{
					Alias:     aliasEntry.Text,
					Endpoint:  endpointEntry.Text,
					AccessKey: accessKeyEntry.Text,
					SecretKey: secretKeyEntry.Text,
				}
				// 验证输入
				if newService.Alias == "" || newService.Endpoint == "" || newService.AccessKey == "" || newService.SecretKey == "" {
					dialog.ShowInformation("提示", "所有字段都不能为空！", sv.window)
					return // 不保存，并保持对话框打开
				}

				sv.configStore.UpdateService(selectedService.Alias, newService)
				sv.saveConfig()
				sv.refreshServiceList()
			}
		}, sv.window)
		d.Resize(fyne.NewSize(400, 250)) // 设置对话框的最小尺寸
		d.Show()
	})

	// 删除服务按钮 (只显示图标)
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
				// 删除后取消选中，避免索引越界
				sv.serviceList.UnselectAll()
				sv.selectedServiceID = -1 // 重置选中 ID
				// TODO: 通知中间和右侧视图清空内容
			}
		}, sv.window)
	})

	// 按钮布局：垂直堆叠
	buttonBox := container.NewVBox(
		addButton,
		editButton,
		deleteButton,
	)

	// 整体布局：服务列表 + 按钮
	return container.NewBorder(nil, buttonBox, nil, nil, sv.serviceList)
}
