package components

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// TappableContainer 是一个可以捕获其背景上的点击事件的容器。
type TappableContainer struct {
	widget.BaseWidget
	Content  fyne.CanvasObject
	OnTapped func()
}

// CreateRenderer 实现了 Widget 接口。
func (c *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.Content)
}

// Tapped 在容器被点击时调用。
func (c *TappableContainer) Tapped(_ *fyne.PointEvent) {
	if c.OnTapped != nil {
		c.OnTapped()
	}
}

// NewTappableContainer 创建一个新的 TappableContainer。
func NewTappableContainer(content fyne.CanvasObject, onTapped func()) *TappableContainer {
	c := &TappableContainer{
		Content:  content,
		OnTapped: onTapped,
	}
	c.ExtendBaseWidget(c)
	return c
}
