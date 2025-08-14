package main

import (
	"fmt"
	"image/color" // 导入 image/color 包用于颜色定义
	"io/ioutil"   // 导入 ioutil 包用于读取文件
	"log"         // 导入 log 包用于日志输出
	"net/url"
	"s3-explorer/config"

	"fyne.io/fyne/v2"           // 导入 fyne 主包
	"fyne.io/fyne/v2/app"       // 导入 fyne 应用包
	"fyne.io/fyne/v2/container" // 导入 fyne 容器包
	"fyne.io/fyne/v2/dialog"    // 导入 fyne 对话框包
	"fyne.io/fyne/v2/theme"     // 导入 fyne 主题包
	"fyne.io/fyne/v2/widget"
	"s3-explorer/s3client" // 导入 s3client 包
	"s3-explorer/ui"       // 导入 ui 包
)

// customTheme 自定义主题结构体
type customTheme struct{}

// Color 返回主题特定颜色
// 实现了 fyne.Theme 接口的 Color 方法
func (t *customTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, variant)
}

// Font 返回自定义字体
// 实现了 fyne.Theme 接口的 Font 方法
func (t *customTheme) Font(textStyle fyne.TextStyle) fyne.Resource {
	// 读取字体文件
	fontData, err := ioutil.ReadFile("assets/font/SourceHanSansSC-Regular.otf")
	if err != nil {
		log.Printf("无法加载字体文件: %v, 将使用默认字体", err)
		// 如果字体加载失败，返回默认字体
		return theme.DefaultTheme().Font(textStyle)
	}
	// 将字体数据封装为 fyne.Resource
	return fyne.NewStaticResource("SourceHanSansSC-Regular.otf", fontData)
}

// Icon 返回主题特定图标资源
// 实现了 fyne.Theme 接口的 Icon 方法
func (t *customTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

// Size 返回主题尺寸
// 实现了 fyne.Theme 接口的 Size 方法
func (t *customTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// showHelpDialog 显示帮助说明对话框
func showHelpDialog(w fyne.Window) {
	helpText := `S3 Explorer 使用说明:

1. 添加服务:
   - 点击左上角的 "+" 按钮。
   - 填写服务别名、Endpoint、Access Key 和 Secret Key。
   - 点击 "添加" 保存。

2. 浏览和操作:
   - 左侧列表选择一个服务。
   - 中间列表会显示存储桶，点击进入。
   - 存储桶不为空时才可以删除，选中的存储桶不为空时删除按钮无法点击。
   - 右侧列表显示文件和文件夹。
   - 使用顶部的按钮进行创建文件夹、上传、下载、删除等操作。
   - 双击文件可进行预览。
   - 将文件或文件夹从系统拖拽到窗口内可直接上传。

3. 键盘快捷键:
   - Ctrl+C: 复制选中的S3对象（文件/文件夹）信息到应用内部
   - Ctrl+V: 粘贴剪贴板中的文件并上传到当前目录，或粘贴已复制的S3对象到当前目录

4. 视图切换:
   - 点击右上角的视图切换按钮可在列表和缩略图模式间切换。
   - 程序会为每个服务记住您的视图偏好。

5. 注意事项:
   - 由于 S3 协议不支持分页，所以分页功能文件夹显示数量可能不准确，但是总文件数是正确的。
   - 分页配置为 0 表示不分页。
`
	content := widget.NewMultiLineEntry()
	content.SetText(helpText)
	content.Wrapping = fyne.TextWrapWord
	content.Disable()

	scrollableContent := container.NewScroll(content)
	d := dialog.NewCustom("使用说明", "关闭", scrollableContent, w)
	d.Resize(fyne.NewSize(500, 400))
	d.Show()
}

// showAboutDialog 显示关于对话框
func showAboutDialog(w fyne.Window) {
	ghURL, _ := url.Parse("https://github.com/fan5487464/s3-explorer")
	gtURL, _ := url.Parse("https://gitee.com/javaTrainee/s3-explorer") // Gitee 仓库地址

	aboutContent := container.NewVBox(
		widget.NewLabel("S3 Explorer"),
		widget.NewLabel("版本: 1.0.0"),
		widget.NewLabel("一个简单的 S3 兼容对象存储桌面浏览器。"),
		widget.NewHyperlink("GitHub 仓库", ghURL),
		widget.NewHyperlink("Gitee 仓库", gtURL),
	)

	dialog.ShowCustom("关于 S3 Explorer", "关闭", aboutContent, w)
}

func main() {
	// 初始化数据库
	if err := config.InitDB(); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}

	// 创建一个新的 Fyne 应用，并指定一个唯一的 ID
	a := app.NewWithID("link.yifan.s3explorer")

	// 设置自定义主题
	a.Settings().SetTheme(&customTheme{})

	// 创建一个新窗口
	w := a.NewWindow("S3 资源管理器")

	// --- 创建主菜单 ---
	helpMenu := fyne.NewMenu("帮助",
		fyne.NewMenuItem("使用说明", func() {
			showHelpDialog(w)
		}),
	)

	aboutMenu := fyne.NewMenu("关于",
		fyne.NewMenuItem("关于 S3 Explorer", func() {
			showAboutDialog(w)
		}),
	)

	mainMenu := fyne.NewMainMenu(helpMenu, aboutMenu)
	w.SetMainMenu(mainMenu)

	// 创建动画管理器实例
	animationManager := ui.NewAnimationManager(w)

	// 创建 UI 视图实例，并传入动画管理器
	objectsView := ui.NewObjectsView(w, animationManager)   // 修改构造函数调用
	bucketsView := ui.NewBucketsView(w, animationManager)   // 修改构造函数调用
	servicesView := ui.NewServicesView(w, animationManager) // 修改构造函数调用

	// --- 设置视图间的交互回调 ---

	// 当对象视图的模式改变时，更新服务视图中的配置
	objectsView.OnViewModeChanged = servicesView.UpdateServiceViewMode

	// 当选中存储桶时，更新对象视图
	bucketsView.OnBucketSelected = func(bucketName string) {
		if bucketsView.S3Client != nil {
			objectsView.SetBucketAndPrefix(bucketsView.S3Client, bucketName, "")
		} else {
			log.Println("S3 客户端未初始化，无法列出对象")
		}
	}

	// 当选中服务时，更新存储桶和对象视图
	servicesView.OnServiceSelected = func(svc config.S3ServiceConfig) {
		objectsView.SetServiceAlias(svc.Alias)

		if svc.Alias == "" && svc.Endpoint == "" && svc.AccessKey == "" {
			bucketsView.SetS3Client(nil)
			objectsView.SetBucketAndPrefix(nil, "", "")
			return
		}

		client, err := s3client.NewS3Client(svc)
		if err != nil {
			log.Printf("创建 S3 客户端失败: %v", err)
			dialog.ShowError(fmt.Errorf("创建 S3 客户端失败: %v", err), w)
			bucketsView.SetS3Client(nil)
			objectsView.SetBucketAndPrefix(nil, "", "")
			return
		}

		// 根据服务的配置设置视图模式
		objectsView.SetViewMode(svc.ViewMode)

		bucketsView.SetS3Client(client)
		objectsView.SetBucketAndPrefix(client, "", "") // 清空对象列表，等待存储桶选择
	}

	// --- 布局设置 ---

	// 内层分割：存储桶(中) | 对象(右)
	innerSplit := container.NewHSplit(
		bucketsView.GetContent(),
		objectsView.GetContent(),
	)
	// 内层分割比例：中间占 1.0 / (1.0 + 8.0) = 1.0 / 9.0
	innerSplit.Offset = 1.0 / 9.0

	// 外层分割：服务(左) | 内层
	content := container.NewHSplit(
		servicesView.GetContent(),
		innerSplit,
	)
	// 外层分割比例：左侧占 1.0 ➗ 10.0 = 0.1
	content.Offset = 0.1

	// 设置窗口内容和大小
	w.SetContent(content)
	w.Resize(fyne.NewSize(1280, 720))

	// 显示并运行窗口
	w.ShowAndRun()
}
