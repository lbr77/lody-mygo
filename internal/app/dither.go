package app

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// The sign-in page's visual, as Lody draws it: a ray-marched jellyfish (the
// Lody mark) as 1-bit ordered dither in the surface's own ink and paper. It
// is ported from Lody's login-dither-visual.tsx: one sample per 3-DIP cell
// into a small canvas, shown pixelated. Without a GPU it is the plain
// surface, as it is without WebGL in Lody.

const ditherCell = 3 // DIPs per dither cell

var jellyfish = shader.MustCompile(`package main

var Time float
var Res vec2
var Mouse vec2
var Paper vec3
var Ink vec3
var Reveal float

// rot is GLSL's mat2(c,-s,s,c)*v.
func rot(v vec2, a float) vec2 {
	c := cos(a)
	s := sin(a)
	return vec2(c*v.x+s*v.y, -s*v.x+c*v.y)
}

func b2(a vec2) float {
	a = floor(a)
	return fract(a.x*0.5 + a.y*a.y*0.75)
}

func b4(a vec2) float { return b2(0.5*a)*0.25 + b2(a) }
func b8(a vec2) float { return b4(0.5*a)*0.25 + b2(a) }

func bell(p vec3) float {
	pu := 1 + 0.08*sin(Time*2.2)
	q := vec3(p.x/pu, p.y*pu, p.z/pu)
	e := q * vec3(1, 1.3, 1)
	d := max((length(e)-1)/1.3, -(q.y + 0.05))
	d = max(d, -((length(e-vec3(0, -0.16, 0)) - 0.84) / 1.3))
	d += 0.025 * sin(atan2(q.z, q.x)*12+Time*3) * (1 - smoothstep(-0.05, 0.25, q.y))
	return d
}

func tent(p vec3) float {
	d := 100000.0
	for i := 0; i < 8; i++ {
		fi := float(i)
		a := fi*0.785 + 0.3
		y := clamp(-p.y, 0, 2.8)
		o := vec2(cos(a), sin(a))*(0.62-y*0.08) + vec2(sin(y*2.3-Time*2+fi), cos(y*2-Time*1.6+fi*1.3))*0.13*y
		d = min(d, length(p-vec3(o.x, -y, o.y))-0.05*(1-y/3.1))
	}
	y := clamp(-p.y, 0, 1.8)
	d = min(d, length(p-vec3(sin(y*3-Time*1.5)*0.12*y, -y, cos(y*2.5-Time*1.3)*0.1*y))-0.15*(1-y/2))
	return d
}

func sdf(p vec3) float {
	p = vec3(p.x, p.y-0.14*sin(Time*0.7), p.z)
	xz := rot(p.xz, (Mouse.x-0.5)*1.4+Time*0.15)
	p = vec3(xz.x, p.y, xz.y)
	yz := rot(p.yz, 0.38+(Mouse.y-0.5)*0.6)
	p = vec3(p.x, yz.x, yz.y)
	return min(bell(p), tent(p))
}

func Fragment(dst vec4, pos vec2) vec4 {
	frag := vec2(pos.x, Res.y-pos.y) // gl_FragCoord: y up
	sp := frag / Res
	uv := (frag - 0.5*Res) / Res.y
	aspect := Res.x / Res.y
	ro := vec3(-0.75*clamp((aspect-1)/0.6, 0, 1), -0.95, 7.4)
	rd := normalize(vec3(uv, -1.7))
	d := 0.0
	hit := false
	for i := 0; i < 80; i++ {
		h := sdf(ro + rd*d)
		if h < 0.002 {
			hit = true
			break
		}
		d += h * 0.8
		if d > 13 {
			break
		}
	}
	l := 0.02 + 0.1*(1-sp.y) + 0.04*sin(sp.x*9+Time*0.4+sin(sp.y*7-Time*0.3)*2)
	if hit {
		p := ro + rd*d
		e := vec2(0.003, 0)
		n := normalize(vec3(sdf(p+e.xyy)-sdf(p-e.xyy), sdf(p+e.yxy)-sdf(p-e.yxy), sdf(p+e.yyx)-sdf(p-e.yyx)))
		dif := max(dot(n, normalize(vec3(-0.5, 0.8, 0.6))), 0)
		rim := pow(1-max(dot(n, -rd), 0), 2)
		l = 0.12 + 0.6*dif + 0.55*rim
	}
	// The entrance: density grows outward from the centre.
	l *= smoothstep(0, 1, Reveal*1.8-length(uv)*0.8)
	on := step(b8(frag)+0.001, clamp(l, 0, 1))
	return vec4(mix(Paper, Ink, on), 1)
}
`)

// dither is the state of the visual across frames.
type dither struct {
	canvas     *ui.Canvas
	cols, rows int
	start      time.Time
	last       time.Time
	mouse      [2]float32 // eased toward the pointer, 0..1, y up
	pointer    [3]float32 // x, y in DIPs, and 1 when over the visual
}

const revealDuration = 1600 * time.Millisecond

// view draws the visual into a rounded surface that fills its parent.
func (d *dither) view(c *ui.Context, p *palette) ui.Element {
	paper := p.Canvas.Mix(p.Text, 0.05)
	ink := p.Text
	if p.Dark {
		ink = p.Text.Mix(ui.RGB(0, 0, 0), 0.12)
	}
	still := c.Preferences().ReduceMotion
	el := ui.Box(c).Grow(1).MinWidth(0).FillHeight().Radius(14).Clip().Background(paper)
	if x, y, ok := el.PointerPosition(); ok {
		d.pointer = [3]float32{x, y, 1}
	} else {
		d.pointer[2] = 0
	}
	return el.Draw(func(pt *ui.Painter, r ui.Rect) {
		if !pt.Accelerated() {
			return // interpreted on the CPU it would take seconds a frame
		}
		cols, rows := max(1, int(r.W/ditherCell)), max(1, int(r.H/ditherCell))
		if d.canvas == nil || cols != d.cols || rows != d.rows {
			d.canvas, d.cols, d.rows = ui.NewCanvas(cols, rows), cols, rows
		}
		now := pt.Now()
		if d.start.IsZero() {
			d.start, d.last = now, now
			d.mouse = [2]float32{0.5, 0.5}
		}
		target := [2]float32{0.5, 0.5}
		if d.pointer[2] == 1 && r.W > 0 && r.H > 0 {
			target = [2]float32{d.pointer[0] / r.W, 1 - d.pointer[1]/r.H}
		}
		// Lody eases 5% a frame at 60 fps.
		ease := float32(1 - pow95(now.Sub(d.last).Seconds()*60))
		d.mouse[0] += (target[0] - d.mouse[0]) * ease
		d.mouse[1] += (target[1] - d.mouse[1]) * ease
		d.last = now
		elapsed := now.Sub(d.start)
		reveal := float32(1)
		if !still {
			reveal = float32(min(1, elapsed.Seconds()/revealDuration.Seconds()))
		}
		t := elapsed.Seconds() + 3
		if still {
			t = 3
		}
		d.canvas.Draw(pt, func(cp *ui.Painter) {
			jellyfish.Draw(cp, ui.Rect{W: float32(cols), H: float32(rows)}, shader.Options{Uniforms: map[string]any{
				"Time":   t,
				"Res":    []float32{float32(cols), float32(rows)},
				"Mouse":  d.mouse[:],
				"Paper":  paper,
				"Ink":    ink,
				"Reveal": reveal,
			}})
		})
		// Whole cells at exactly ditherCell DIPs, so the pattern doesn't
		// swim as the window resizes; the sliver left is plain paper.
		pt.Image(d.canvas.Bitmap().Pixelated(), ui.Rect{X: r.X, Y: r.Y, W: float32(cols * ditherCell), H: float32(rows * ditherCell)}, ui.FillBox)
		if !still {
			pt.AnimationFrame()
		}
	})
}

// pow95 is 0.95 to the power n, n frames of Lody's easing.
func pow95(n float64) float64 {
	if n <= 0 {
		return 1
	}
	r := 1.0
	for n >= 1 {
		r *= 0.95
		n--
	}
	return r * (1 - 0.05*n)
}
