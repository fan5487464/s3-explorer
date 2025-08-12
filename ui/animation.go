package ui

import (
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// AnimationManager 管理UI动画效果
type AnimationManager struct {
	window fyne.Window
}

// NewAnimationManager 创建新的动画管理器
func NewAnimationManager(window fyne.Window) *AnimationManager {
	return &AnimationManager{
		window: window,
	}
}

// AnimateFade 执行淡入淡出动画
func (am *AnimationManager) AnimateFade(obj fyne.CanvasObject, duration time.Duration, from, to float32, callback func()) {
	animation := &fyne.Animation{
		Duration: duration,
		Tick: func(done float32) {
			alpha := from + (to-from)*done
			if c, ok := obj.(*canvas.Rectangle); ok {
				// 使用类型断言获取具体的颜色类型并修改Alpha值
				if rgba, ok := c.FillColor.(color.RGBA); ok {
					rgba.A = uint8(alpha * 255)
					c.FillColor = rgba
				} else if nrgba, ok := c.FillColor.(color.NRGBA); ok {
					nrgba.A = uint8(alpha * 255)
					c.FillColor = nrgba
				}
				c.Refresh()
			}
		},
	}
	animation.Start()
	// 动画结束后调用回调函数
	if callback != nil {
		go func() {
			time.Sleep(duration)
			callback()
		}()
	}
}

// AnimateScale 执行缩放动画
func (am *AnimationManager) AnimateScale(obj fyne.CanvasObject, duration time.Duration, from, to float32, callback func()) {
	originalSize := obj.Size()
	animation := &fyne.Animation{
		Duration: duration,
		Tick: func(done float32) {
			scale := from + (to-from)*done
			newSize := fyne.NewSize(originalSize.Width*scale, originalSize.Height*scale)
			obj.Resize(newSize)
		},
	}
	animation.Start()
	// 动画结束后调用回调函数
	if callback != nil {
		go func() {
			time.Sleep(duration)
			callback()
		}()
	}
}

// AnimateSlide 执行滑动动画
func (am *AnimationManager) AnimateSlide(obj fyne.CanvasObject, duration time.Duration, from, to fyne.Position, callback func()) {
	animation := &fyne.Animation{
		Duration: duration,
		Tick: func(done float32) {
			x := from.X + (to.X-from.X)*done
			y := from.Y + (to.Y-from.Y)*done
			obj.Move(fyne.NewPos(x, y))
		},
	}
	animation.Start()
	// 动画结束后调用回调函数
	if callback != nil {
		go func() {
			time.Sleep(duration)
			callback()
		}()
	}
}

// CreatePulseAnimation 创建脉冲动画效果
func (am *AnimationManager) CreatePulseAnimation(obj fyne.CanvasObject, duration time.Duration) *fyne.Animation {
	return &fyne.Animation{
		Duration: duration,
		Tick: func(done float32) {
			// 使用math.Sin创建正弦波效果
			scale := 1.0 + 0.1*float32(0.5*(1+math.Sin(2*math.Pi*float64(done))))
			originalSize := obj.Size()
			newSize := fyne.NewSize(originalSize.Width*scale, originalSize.Height*scale)
			obj.Resize(newSize)
		},
		RepeatCount: fyne.AnimationRepeatForever,
	}
}

// CreateBounceAnimation 创建弹跳动画效果
func (am *AnimationManager) CreateBounceAnimation(obj fyne.CanvasObject, duration time.Duration, distance float32) *fyne.Animation {
	originalPos := obj.Position()
	return &fyne.Animation{
		Duration: duration,
		Tick: func(done float32) {
			// 使用二次函数创建弹跳效果
			var offsetY float32
			if done < 0.5 {
				// 上升阶段
				t := done * 2
				offsetY = -distance * t * t
			} else {
				// 下降阶段
				t := (done - 0.5) * 2
				offsetY = -distance * (1 - t*t)
			}
			obj.Move(fyne.NewPos(originalPos.X, originalPos.Y+offsetY))
		},
		RepeatCount: fyne.AnimationRepeatForever,
	}
}
