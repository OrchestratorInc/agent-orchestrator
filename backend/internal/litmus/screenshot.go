package litmus

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

const pageThemeJS = `document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')`

func (e *Engine) theme(ctx context.Context) (theme string, err error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	p, err := e.pageCDP(ctx)
	if err != nil {
		return "", err
	}
	if p.conn != nil {
		defer func() { _ = p.conn.CloseNow() }()
	}
	err = p.evaluate(ctx, pageThemeJS, &theme)
	return theme, err
}

// AO's transparent shell sits over the native window's sidebar colour. Resolve
// that token in the current theme, falling back to an opaque page background.
const windowBackgroundJS = `(() => {
 const root = document.documentElement, style = getComputedStyle(root);
 let bg = style.getPropertyValue('--color-bg-sidebar').trim();
 if (!bg) for (const node of [document.body, root]) {
   if (!node) continue;
   const value = getComputedStyle(node).backgroundColor;
   if (value !== 'transparent' && !/rgba\([^)]*,\s*0\s*\)/.test(value)) { bg = value; break; }
 }
	const fallback = (` + pageThemeJS + `) === 'dark' ? '#0c0c0e' : '#fbfbfb';
	if (!bg) bg = fallback;
	const canvas = document.createElement('canvas'); canvas.width = canvas.height = 1;
	const c = canvas.getContext('2d'); c.fillStyle = fallback; c.fillStyle = bg; c.fillRect(0,0,1,1);
 if (c.getImageData(0,0,1,1).data[3] !== 255) {
   c.clearRect(0,0,1,1); c.fillStyle = fallback; c.fillRect(0,0,1,1);
 }
 return Array.from(c.getImageData(0,0,1,1).data);
})()`

// opaquePNG keeps fully opaque pictures unchanged and composites alpha over the
// background actually behind the page. Failures leave the original file intact.
func opaquePNG(path string, background func() (color.Color, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if opaque, ok := src.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		return nil
	}
	bg, err := background()
	if err != nil {
		return err
	}
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return err
	}
	return writeAtomic(path, out.Bytes())
}

func rgbaBackground(values []uint8) (color.Color, error) {
	if len(values) != 4 || values[3] != 255 {
		return nil, fmt.Errorf("page returned no opaque window background")
	}
	return color.RGBA{R: values[0], G: values[1], B: values[2], A: 255}, nil
}

func (e *Engine) flatten(ctx context.Context, path string) error {
	return opaquePNG(path, func() (color.Color, error) {
		ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
		defer cancel()
		p, err := e.pageCDP(ctx)
		if err != nil {
			return nil, err
		}
		if p.conn != nil {
			defer func() { _ = p.conn.CloseNow() }()
		}
		var values []uint8
		if err := p.evaluate(ctx, windowBackgroundJS, &values); err != nil {
			return nil, err
		}
		return rgbaBackground(values)
	})
}
