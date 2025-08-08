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

	"s3-explorer/s3client"
)

// BucketsView 结构体用于管理中间的存储桶列表视图
type BucketsView struct {
	window           fyne.Window
	S3Client         *s3client.S3Client          // 修正：改为大写 S3Client，使其可导出
	bucketList       *widget.List                // 用于显示存储桶列表的 Fyne 列表组件
	buckets          []string                    // 存储桶名称列表
	selectedBucketID widget.ListItemID           // 存储当前选中的存储桶 ID
	deleteButton     *widget.Button              // 删除按钮，用于控制启用/禁用状态
	loadingIndicator *widget.ProgressBarInfinite // 加载指示器

	// OnBucketSelected 是一个回调函数，当用户选择一个存储桶时触发
	// 参数是选中的存储桶名称
	OnBucketSelected func(bucketName string)
}

// NewBucketsView 创建并返回一个新的 BucketsView 实例
func NewBucketsView(w fyne.Window) *BucketsView {
	bv := &BucketsView{
		window:           w,
		selectedBucketID: -1,                              // 初始状态为未选中
		loadingIndicator: widget.NewProgressBarInfinite(), // 初始化加载指示器
	}
	bv.loadingIndicator.Hide() // 默认隐藏
	return bv
}

// SetS3Client 设置 S3 客户端，并刷新存储桶列表
func (bv *BucketsView) SetS3Client(client *s3client.S3Client) {
	bv.S3Client = client // 修正：使用大写 S3Client
	bv.loadBuckets()
}

// loadBuckets 加载存储桶列表
func (bv *BucketsView) loadBuckets() {
	if bv.S3Client == nil { // 修正：使用大写 S3Client
		bv.buckets = []string{} // 没有客户端，清空列表
		bv.refreshBucketList()
		bv.checkDeleteButtonState() // 刷新删除按钮状态
		return
	}

	bv.loadingIndicator.Show() // 显示加载指示器
	go func() {
		buckets, err := bv.S3Client.ListBuckets() // 修正：使用大写 S3Client
		// 所有 UI 更新都必须在 Fyne 主线程中执行
		fyne.Do(func() {
			bv.loadingIndicator.Hide() // 隐藏加载指示器
			if err != nil {
				log.Printf("列出存储桶失败: %v", err)
				dialog.ShowError(fmt.Errorf("列出存储桶失败: %v", err), bv.window)
				bv.buckets = []string{} // 加载失败，清空列表
			} else {
				bv.buckets = buckets
			}
			bv.refreshBucketList()
			bv.checkDeleteButtonState() // 刷新删除按钮状态
		})
	}()
}

// refreshBucketList 刷新存储桶列表显示
func (bv *BucketsView) refreshBucketList() {
	if bv.bucketList == nil {
		return
	}
	bv.bucketList.Refresh()
}

// checkDeleteButtonState 检查并设置删除按钮的启用状态
func (bv *BucketsView) checkDeleteButtonState() {
	if bv.deleteButton == nil {
		return
	}

	// 默认禁用
	bv.deleteButton.Disable()

	if bv.S3Client == nil || bv.selectedBucketID == -1 || bv.selectedBucketID >= len(bv.buckets) {
		return // 没有 S3 客户端或没有选中存储桶
	}

	selectedBucket := bv.buckets[bv.selectedBucketID]

	go func() {
		isEmpty, err := bv.S3Client.IsBucketEmpty(selectedBucket)
		fyne.Do(func() {
			if err != nil {
				log.Printf("检查存储桶是否为空失败: %v", err)
				// 检查失败，保持禁用状态或显示错误
				bv.deleteButton.Disable()
			} else {
				if isEmpty {
					bv.deleteButton.Enable() // 存储桶为空，启用删除按钮
				} else {
					bv.deleteButton.Disable() // 存储桶不为空，禁用删除按钮
				}
			}
		})
	}()
}

// GetContent 返回 BucketsView 的 Fyne UI 内容
func (bv *BucketsView) GetContent() fyne.CanvasObject {
	bv.bucketList = widget.NewList(
		func() int {
			return len(bv.buckets)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("存储桶名称") // 列表项的模板
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// 更新列表项内容
			label := obj.(*widget.Label)
			label.SetText(bv.buckets[id])
		},
	)

	// 设置列表项点击事件
	bv.bucketList.OnSelected = func(id widget.ListItemID) {
		bv.selectedBucketID = id // 记录选中的 ID
		if bv.OnBucketSelected != nil {
			bv.OnBucketSelected(bv.buckets[id])
		}
		bv.checkDeleteButtonState() // 选中项改变时，刷新删除按钮状态
	}

	// 创建存储桶按钮
	createBucketButton := widget.NewButtonWithIcon("创建", theme.ContentAddIcon(), func() {
		if bv.S3Client == nil {
			dialog.ShowInformation("提示", "请先选择一个 S3 服务。", bv.window)
			return
		}
		entry := widget.NewEntry()
		dialog.ShowForm("创建存储桶", "创建", "取消", []*widget.FormItem{
			widget.NewFormItem("存储桶名称", entry),
		}, func(confirmed bool) {
			if confirmed {
				bucketName := entry.Text
				if bucketName == "" {
					dialog.ShowInformation("提示", "存储桶名称不能为空。", bv.window)
					return
				}
				go func() {
					err := bv.S3Client.CreateBucket(bucketName)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("创建存储桶失败: %v", err), bv.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("存储桶 \"%s\" 创建成功！", bucketName), bv.window)
							bv.loadBuckets() // 刷新列表
						}
					})
				}()
			}
		}, bv.window)
	})

	// 删除存储桶按钮
	bv.deleteButton = widget.NewButtonWithIcon("删除", theme.DeleteIcon(), func() {
		// 这里的检查是双重保险，因为按钮的启用状态已经控制了点击
		if bv.S3Client == nil || bv.selectedBucketID == -1 || bv.selectedBucketID >= len(bv.buckets) {
			dialog.ShowInformation("提示", "请先选择一个要删除的存储桶。", bv.window)
			return
		}
		selectedBucket := bv.buckets[bv.selectedBucketID]

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除存储桶 \"%s\" 吗？", selectedBucket), func(confirmed bool) { // 简化提示
			if confirmed {
				go func() {
					err := bv.S3Client.DeleteBucket(selectedBucket)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("删除存储桶失败: %v", err), bv.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("存储桶 \"%s\" 删除成功！", selectedBucket), bv.window)
							bv.loadBuckets() // 刷新列表
							// TODO: 通知右侧视图清空内容
						}
					})
				}()
			}
		}, bv.window)
	})
	bv.deleteButton.Disable() // 初始状态为禁用

	// 按钮布局：水平排列，放置在列表上方
	buttonBox := container.NewHBox(
		createBucketButton,
		bv.deleteButton,
		layout.NewSpacer(),  // 将按钮推到左侧
		bv.loadingIndicator, // 加载指示器
	)

	// 整体布局：按钮 + 分隔符 + 存储桶列表
	return container.NewBorder(buttonBox, nil, nil, nil, container.NewVBox(widget.NewSeparator()), bv.bucketList)
}
