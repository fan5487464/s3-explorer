package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ShowToast 在窗口底部显示一条简短的自动关闭消息。
func ShowToast(window fyne.Window, message string) {
	// 带背景的Toast内容容器
	content := container.NewPadded(widget.NewLabel(message))
	background := canvas.NewRectangle(theme.OverlayBackgroundColor())
	background.CornerRadius = 5
	toastContainer := container.NewStack(background, content)

	// 包含Toast内容的弹出式窗口
	popup := widget.NewPopUp(toastContainer, window.Canvas())

	// 将弹出窗口定位在窗口底部中心
	toastContainer.Resize(toastContainer.MinSize())
	popup.Move(fyne.NewPos(
		(window.Canvas().Size().Width-toastContainer.Size().Width)/2,
		window.Canvas().Size().Height-toastContainer.Size().Height-40, // 离底部40像素
	))

	popup.Show()

	// 设置一个计时器，2秒后隐藏弹出窗口
	time.AfterFunc(2*time.Second, popup.Hide)
}
