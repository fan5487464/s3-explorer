package ui

import (
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ThinProgressBar 是一个简单的、细长的、不确定的进度条小部件。
type ThinProgressBar struct {
	widget.BaseWidget

	line *canvas.Rectangle
	anim *fyne.Animation
}

// NewThinProgressBar 创建一个新的细长进度条。
func NewThinProgressBar() *ThinProgressBar {
	p := &ThinProgressBar{}
	p.ExtendBaseWidget(p)
	p.line = canvas.NewRectangle(theme.PrimaryColor())
	p.Hide()
	return p
}

// CreateRenderer 是 Fyne 的一个私有方法，用于将此小部件链接到其渲染器。
func (p *ThinProgressBar) CreateRenderer() fyne.WidgetRenderer {
	return &thinProgressBarRenderer{
		progress: p,
		objects:  []fyne.CanvasObject{p.line},
	}
}

// MinSize 返回此小部件可以占用的最小尺寸。
func (p *ThinProgressBar) MinSize() fyne.Size {
	return fyne.NewSize(20, 2) // 2像素高
}

// Show 显示此小部件并启动动画。
func (p *ThinProgressBar) Show() {
	p.BaseWidget.Show()

	if p.anim == nil {
		p.anim = fyne.NewAnimation(time.Second*1, func(val float32) {
			if p.Size().Width == 0 {
				return
			}
			barWidth := p.Size().Width / 4 // 移动条是总宽度的 1/4
			offset := val*(p.Size().Width+barWidth) - barWidth

			p.line.Move(fyne.NewPos(offset, 0))
			p.line.Resize(fyne.NewSize(barWidth, p.MinSize().Height))
		})
		p.anim.RepeatCount = fyne.AnimationRepeatForever
	}
	p.anim.Start()
}

// Hide 隐藏此小部件并停止动画。
func (p *ThinProgressBar) Hide() {
	if p.anim != nil {
		p.anim.Stop()
	}
	p.BaseWidget.Hide()
}

// PulseAnimation creates a pulsing effect for the progress bar
func (p *ThinProgressBar) PulseAnimation() *fyne.Animation {
	return &fyne.Animation{
		Duration: time.Millisecond * 500,
		Tick: func(done float32) {
			// Pulsing effect by changing the height
			height := 2 + float32(1-math.Cos(float64(done)*2*math.Pi))*2
			p.line.Resize(fyne.NewSize(p.line.Size().Width, height))
		},
		RepeatCount: fyne.AnimationRepeatForever,
	}
}

type thinProgressBarRenderer struct {
	progress *ThinProgressBar
	objects  []fyne.CanvasObject
}

func (r *thinProgressBarRenderer) Destroy() {}

func (r *thinProgressBarRenderer) Layout(size fyne.Size) {
	// 动画将处理线条的布局。
}

func (r *thinProgressBarRenderer) MinSize() fyne.Size {
	return r.progress.MinSize()
}

func (r *thinProgressBarRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *thinProgressBarRenderer) Refresh() {
	if r.progress.Visible() {
		r.progress.line.FillColor = theme.PrimaryColor()
		r.progress.line.Show()
	} else {
		r.progress.line.Hide()
	}
	r.progress.line.Refresh()
	canvas.Refresh(r.progress)
}