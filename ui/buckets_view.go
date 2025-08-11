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

	"s3-explorer/s3client"
)

// bucketListEntry 是存储桶列表的自定义列表项
type bucketListEntry struct {
	widget.BaseWidget
	label    *widget.Label
	id       widget.ListItemID
	bv       *BucketsView
	selected bool
}

func (e *bucketListEntry) Tapped(_ *fyne.PointEvent) {
	e.bv.handleBucketTapped(e.id)
}

func (e *bucketListEntry) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	return &bucketListEntryRenderer{
		entry:      e,
		background: bg,
		content:    container.NewStack(bg, e.label),
	}
}

// bucketListEntryRenderer 自定义渲染器
type bucketListEntryRenderer struct {
	entry      *bucketListEntry
	background *canvas.Rectangle
	content    *fyne.Container
}

func (r *bucketListEntryRenderer) Destroy() {}
func (r *bucketListEntryRenderer) Layout(s fyne.Size) {
	r.content.Resize(s)
}
func (r *bucketListEntryRenderer) MinSize() fyne.Size {
	return r.content.MinSize()
}
func (r *bucketListEntryRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.content}
}
func (r *bucketListEntryRenderer) Refresh() {
	if r.entry.selected {
		r.background.FillColor = theme.SelectionColor()
	} else {
		r.background.FillColor = color.Transparent
	}
	r.background.Refresh()
}

// BucketsView 结构体用于管理中间的存储桶列表视图
type BucketsView struct {
	window           fyne.Window
	S3Client         *s3client.S3Client
	bucketList       *widget.List
	buckets          []string
	selectedBucketID widget.ListItemID
	deleteButton     *widget.Button
	loadingIndicator *widget.ProgressBarInfinite

	OnBucketSelected func(bucketName string)
}

// NewBucketsView 创建并返回一个新的 BucketsView 实例
func NewBucketsView(w fyne.Window) *BucketsView {
	bv := &BucketsView{
		window:           w,
		selectedBucketID: -1,
		loadingIndicator: widget.NewProgressBarInfinite(),
	}
	bv.loadingIndicator.Hide()
	return bv
}

func (bv *BucketsView) handleBucketTapped(id widget.ListItemID) {
	if bv.selectedBucketID == id {
		bv.selectedBucketID = -1
		if bv.OnBucketSelected != nil {
			bv.OnBucketSelected("") // 清空对象列表
		}
	} else {
		bv.selectedBucketID = id
		if bv.OnBucketSelected != nil {
			bv.OnBucketSelected(bv.buckets[id])
		}
	}
	bv.bucketList.Refresh()
	bv.checkDeleteButtonState()
}

// SetS3Client 设置 S3 客户端，并刷新存储桶列表
func (bv *BucketsView) SetS3Client(client *s3client.S3Client) {
	bv.S3Client = client
	bv.selectedBucketID = -1 // 重置选中状态
	bv.loadBuckets()
}

// loadBuckets 加载存储桶列表
func (bv *BucketsView) loadBuckets() {
	if bv.S3Client == nil {
		bv.buckets = []string{}
		bv.refreshBucketList()
		bv.checkDeleteButtonState()
		return
	}

	bv.loadingIndicator.Show()
	go func() {
		buckets, err := bv.S3Client.ListBuckets()
		fyne.Do(func() {
			bv.loadingIndicator.Hide()
			if err != nil {
				log.Printf("列出存储桶失败: %v", err)
				dialog.ShowError(fmt.Errorf("列出存储桶失败: %v", err), bv.window)
				bv.buckets = []string{}
			} else {
				bv.buckets = buckets
			}
			bv.refreshBucketList()
			bv.checkDeleteButtonState()
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

	bv.deleteButton.Disable()

	if bv.S3Client == nil || bv.selectedBucketID == -1 || bv.selectedBucketID >= len(bv.buckets) {
		return
	}

	selectedBucket := bv.buckets[bv.selectedBucketID]

	go func() {
		isEmpty, err := bv.S3Client.IsBucketEmpty(selectedBucket)
		fyne.Do(func() {
			if err != nil {
				log.Printf("检查存储桶是否为空失败: %v", err)
				bv.deleteButton.Disable()
			} else {
				if isEmpty {
					bv.deleteButton.Enable()
				} else {
					bv.deleteButton.Disable()
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
			entry := &bucketListEntry{
				label: widget.NewLabel("存储桶名称"),
				bv:    bv,
			}
			entry.ExtendBaseWidget(entry)
			return entry
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			entry := obj.(*bucketListEntry)
			entry.id = id
			entry.label.SetText(bv.buckets[id])
			entry.selected = bv.selectedBucketID == id
			entry.Refresh()
		},
	)

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
							bv.loadBuckets()
						}
					})
				}()
			}
		}, bv.window)
	})

	// 删除存储桶按钮
	bv.deleteButton = widget.NewButtonWithIcon("删除", theme.DeleteIcon(), func() {
		if bv.S3Client == nil || bv.selectedBucketID == -1 || bv.selectedBucketID >= len(bv.buckets) {
			dialog.ShowInformation("提示", "请先选择一个要删除的存储桶。", bv.window)
			return
		}
		selectedBucket := bv.buckets[bv.selectedBucketID]

		dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除存储桶 \"%s\" 吗？", selectedBucket), func(confirmed bool) {
			if confirmed {
				go func() {
					err := bv.S3Client.DeleteBucket(selectedBucket)
					fyne.Do(func() {
						if err != nil {
							dialog.ShowError(fmt.Errorf("删除存储桶失败: %v", err), bv.window)
						} else {
							dialog.ShowInformation("成功", fmt.Sprintf("存储桶 \"%s\" 删除成功！", selectedBucket), bv.window)
							bv.loadBuckets()
						}
					})
				}()
			}
		}, bv.window)
	})
	bv.deleteButton.Disable()

	buttonBox := container.NewHBox(
		layout.NewSpacer(),
		createBucketButton,
		layout.NewSpacer(),
		bv.deleteButton,
		layout.NewSpacer(),
		bv.loadingIndicator,
		layout.NewSpacer(),
	)

	return container.NewBorder(buttonBox, nil, nil, nil, container.NewVBox(widget.NewSeparator()), bv.bucketList)
}
