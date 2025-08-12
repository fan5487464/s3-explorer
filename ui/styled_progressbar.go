package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// StyledProgressBar 是一个美化版的进度条
type StyledProgressBar struct {
	widget.BaseWidget

	value     float64
	min       float64
	max       float64
	infinite  bool
	indicator *canvas.Rectangle
	background *canvas.Rectangle
	animation *fyne.Animation
}

// NewStyledProgressBar 创建一个新的美化进度条
func NewStyledProgressBar() *StyledProgressBar {
	p := &StyledProgressBar{
		min:       0,
		max:       1,
		value:     0,
		background: canvas.NewRectangle(color.NRGBA{R: 200, G: 200, B: 200, A: 100}),
		indicator: canvas.NewRectangle(theme.PrimaryColor()),
	}
	p.ExtendBaseWidget(p)
	return p
}

// NewStyledProgressBarInfinite 创建一个新的无限循环进度条
func NewStyledProgressBarInfinite() *StyledProgressBar {
	p := NewStyledProgressBar()
	p.infinite = true
	return p
}

// SetValue 设置进度值
func (p *StyledProgressBar) SetValue(v float64) {
	if p.infinite {
		return
	}
	p.value = v
	p.Refresh()
}

// Value 返回当前进度值
func (p *StyledProgressBar) Value() float64 {
	return p.value
}

// Min 返回最小值
func (p *StyledProgressBar) Min() float64 {
	return p.min
}

// Max 返回最大值
func (p *StyledProgressBar) Max() float64 {
	return p.max
}

// Show 显示进度条并启动动画（如果是无限模式）
func (p *StyledProgressBar) Show() {
	p.BaseWidget.Show()
	
	if p.infinite && p.animation == nil {
		p.animation = fyne.NewAnimation(time.Second*1, func(val float32) {
			if p.Size().Width == 0 {
				return
			}
			barWidth := p.Size().Width / 3
			offset := val*(p.Size().Width+barWidth) - barWidth

			p.indicator.Move(fyne.NewPos(offset, 0))
			p.indicator.Resize(fyne.NewSize(barWidth, p.MinSize().Height))
		})
		p.animation.RepeatCount = fyne.AnimationRepeatForever
		p.animation.Start()
	}
}

// Hide 隐藏进度条并停止动画
func (p *StyledProgressBar) Hide() {
	if p.animation != nil {
		p.animation.Stop()
		p.animation = nil
	}
	p.BaseWidget.Hide()
}

// CreateRenderer 创建渲染器
func (p *StyledProgressBar) CreateRenderer() fyne.WidgetRenderer {
	objects := []fyne.CanvasObject{p.background, p.indicator}
	return &styledProgressBarRenderer{
		progress: p,
		objects:  objects,
	}
}

// MinSize 返回最小尺寸
func (p *StyledProgressBar) MinSize() fyne.Size {
	return fyne.NewSize(100, 8)
}

type styledProgressBarRenderer struct {
	progress *StyledProgressBar
	objects  []fyne.CanvasObject
}

func (r *styledProgressBarRenderer) Destroy() {}

func (r *styledProgressBarRenderer) Layout(size fyne.Size) {
	r.progress.background.Resize(size)
	
	if !r.progress.infinite {
		progressWidth := float32(0.0)
		if r.progress.max != r.progress.min {
			progressWidth = float32(float64(size.Width) * (r.progress.value - r.progress.min) / (r.progress.max - r.progress.min))
		}
		r.progress.indicator.Resize(fyne.NewSize(progressWidth, size.Height))
		r.progress.indicator.Move(fyne.NewPos(0, 0))
	}
	// 无限模式的布局由动画处理
}

func (r *styledProgressBarRenderer) MinSize() fyne.Size {
	return r.progress.MinSize()
}

func (r *styledProgressBarRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *styledProgressBarRenderer) Refresh() {
	// 更新颜色
	r.progress.background.FillColor = color.NRGBA{R: 200, G: 200, B: 200, A: 100}
	r.progress.indicator.FillColor = theme.PrimaryColor()
	
	// 刷新对象
	r.progress.background.Refresh()
	r.progress.indicator.Refresh()
	
	// 触发重新布局
	r.Layout(r.progress.Size())
}