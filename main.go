package main

import (
	"fmt"
	"fyne.io/fyne/v2/dialog"
	"image/color" // 导入 image/color 包用于颜色定义
	"io/ioutil"   // 导入 ioutil 包用于读取文件
	"log"         // 导入 log 包用于日志输出
	"s3-explorer/config"

	"fyne.io/fyne/v2"           // 导入 fyne 主包
	"fyne.io/fyne/v2/app"       // 导入 fyne 应用包
	"fyne.io/fyne/v2/container" // 导入 fyne 容器包
	"fyne.io/fyne/v2/theme"     // 导入 fyne 主题包
	"s3-explorer/s3client"      // 导入 s3client 包
	"s3-explorer/ui"            // 导入 ui 包
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

func main() {
	// 创建一个新的 Fyne 应用，并指定一个唯一的 ID
	a := app.NewWithID("com.yourcompany.s3explorer")

	// 设置自定义主题
	a.Settings().SetTheme(&customTheme{})

	// 创建一个新窗口
	w := a.NewWindow("S3 资源管理器")

	// 创建 UI 视图实例
	objectsView := ui.NewObjectsView(w)
	bucketsView := ui.NewBucketsView(w)
	servicesView := ui.NewServicesView(w)

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
	// 内层分割比例：中间占 1.5 / (1.5 + 7) = 1.5 / 8.5
	innerSplit.Offset = 1.0 / 9.0

	// 外层分割：服务(左) | 内层
	content := container.NewHSplit(
		servicesView.GetContent(),
		innerSplit,
	)
	// 外层分割比例：左侧占 1.5 / 10.0 = 0.15
	content.Offset = 0.1

	// 设置窗口内容和大小
	w.SetContent(content)
	w.Resize(fyne.NewSize(1280, 720))

	// 显示并运行窗口
	w.ShowAndRun()
}
