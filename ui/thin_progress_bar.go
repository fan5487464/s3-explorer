package ui

import (
	"image/color"
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
	// 用于淡入动画
	fadeOverlay *canvas.Rectangle
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
	// 初始化 fadeOverlay
	p.fadeOverlay = canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	p.fadeOverlay.FillColor = theme.Color(theme.ColorNameBackground)
	p.fadeOverlay.Hide()
	
	// 确保 line 在最上层
	return &thinProgressBarRenderer{
		progress: p,
		objects:  []fyne.CanvasObject{p.fadeOverlay, p.line}, // fadeOverlay 在 line 下面
	}
}

// MinSize 返回此小部件可以占用的最小尺寸。
func (p *ThinProgressBar) MinSize() fyne.Size {
	return fyne.NewSize(20, 2) // 2像素高
}

// Show 显示此小部件并启动动画。
func (p *ThinProgressBar) Show() {
	p.BaseWidget.Show()

	// 淡入动画逻辑
	if p.fadeOverlay != nil {
		p.fadeOverlay.FillColor = theme.Color(theme.ColorNameBackground)
		p.fadeOverlay.Resize(p.Size())
		p.fadeOverlay.Show()
		
		// 创建淡入动画 (改变 fadeOverlay 的透明度)
		bgColor := theme.Color(theme.ColorNameBackground)
		var fadeColor color.NRGBA
		if rgba, ok := bgColor.(color.RGBA); ok {
			fadeColor = color.NRGBA{R: rgba.R, G: rgba.G, B: rgba.B, A: 0}
		} else if nrgba, ok := bgColor.(color.NRGBA); ok {
			fadeColor = color.NRGBA{R: nrgba.R, G: nrgba.G, B: nrgba.B, A: 0}
		} else {
			fadeColor = color.NRGBA{R: 0, G: 0, B: 0, A: 0}
		}
		
		fadeAnim := fyne.NewAnimation(time.Millisecond*200, func(done float32) {
			// 从完全透明 (Alpha=0) 到不透明 (Alpha=255)
			alpha := uint8(done * 255)
			fadeColor.A = alpha
			p.fadeOverlay.FillColor = fadeColor
			p.fadeOverlay.Refresh()
			
			// 动画完成后隐藏 fadeOverlay 并显示进度条
			if done == 1.0 {
				p.fadeOverlay.Hide()
				p.line.Show()
				p.Refresh() // 刷新整个组件
			}
		})
		fadeAnim.Start()
	} else {
		p.line.Show()
	}

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
	// 同时隐藏 fadeOverlay (如果存在)
	if p.fadeOverlay != nil {
		p.fadeOverlay.Hide()
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
	// 同时更新 fadeOverlay 的大小
	if r.progress.fadeOverlay != nil {
		r.progress.fadeOverlay.Resize(size)
	}
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
	// 刷新 fadeOverlay
	if r.progress.fadeOverlay != nil {
		r.progress.fadeOverlay.Refresh()
	}
	canvas.Refresh(r.progress)
}