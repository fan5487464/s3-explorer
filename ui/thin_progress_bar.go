package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ThinProgressBar is a simple, thin, indeterminate progress bar widget.
type ThinProgressBar struct {
	widget.BaseWidget

	line *canvas.Rectangle
	anim *fyne.Animation
}

// NewThinProgressBar creates a new thin progress bar.
func NewThinProgressBar() *ThinProgressBar {
	p := &ThinProgressBar{}
	p.ExtendBaseWidget(p)
	p.line = canvas.NewRectangle(theme.PrimaryColor())
	p.Hide()
	return p
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (p *ThinProgressBar) CreateRenderer() fyne.WidgetRenderer {
	return &thinProgressBarRenderer{
		progress: p,
		objects:  []fyne.CanvasObject{p.line},
	}
}

// MinSize returns the smallest size this widget can take.
func (p *ThinProgressBar) MinSize() fyne.Size {
	return fyne.NewSize(20, 2) // 2 pixels high
}

// Show this widget and start the animation.
func (p *ThinProgressBar) Show() {
	p.BaseWidget.Show()

	if p.anim == nil {
		p.anim = fyne.NewAnimation(time.Second*1, func(val float32) {
			if p.Size().Width == 0 {
				return
			}
			barWidth := p.Size().Width / 4 // The moving bar is 1/4 of the total width
			offset := val*(p.Size().Width+barWidth) - barWidth

			p.line.Move(fyne.NewPos(offset, 0))
			p.line.Resize(fyne.NewSize(barWidth, p.MinSize().Height))
		})
		p.anim.RepeatCount = fyne.AnimationRepeatForever
	}
	p.anim.Start()
}

// Hide this widget and stop the animation.
func (p *ThinProgressBar) Hide() {
	if p.anim != nil {
		p.anim.Stop()
	}
	p.BaseWidget.Hide()
}

type thinProgressBarRenderer struct {
	progress *ThinProgressBar
	objects  []fyne.CanvasObject
}

func (r *thinProgressBarRenderer) Destroy() {}

func (r *thinProgressBarRenderer) Layout(size fyne.Size) {
	// The animation will handle the layout of the line.
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
