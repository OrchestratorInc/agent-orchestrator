package litmus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

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
	const fallback = (root.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')) === 'dark' ? '#0c0c0e' : '#fbfbfb';
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

const themePairIgnored = "theme pair not taken: the app ignored the system theme"

const themeStateJS = `(() => {
 const root = document.documentElement;
 return {background:getComputedStyle(root).backgroundColor, theme:root.dataset.theme || '',
   preference:matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'};
})()`

func themePaintJS(background, theme string) string {
	prior, _ := json.Marshal(map[string]string{"background": background, "theme": theme})
	return `new Promise(resolve => {
 const prior = ` + string(prior) + `, root = document.documentElement, deadline = performance.now() + 1500;
 const poll = () => {
   if (getComputedStyle(root).backgroundColor !== prior.background || (root.dataset.theme || '') !== prior.theme) {
     requestAnimationFrame(() => resolve(true));
   } else if (performance.now() >= deadline) resolve(false);
   else setTimeout(poll, Math.min(25, deadline - performance.now()));
 };
 poll();
})`
}

// themePair captures both system preferences before check end persists its
// result. It restores Litmus's previous media override even after a failure.
func (s *Service) themePair(ctx context.Context, e *Engine, id string) (shots []ThemeShot, note string) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	p, err := e.pageCDP(ctx)
	if err != nil {
		return nil, "theme pair not taken: " + err.Error()
	}
	if p.conn != nil {
		defer func() { _ = p.conn.CloseNow() }()
	}
	previous := e.theme
	set := func(ctx context.Context, value string) error {
		features := []map[string]string{}
		if value != "" {
			features = append(features, map[string]string{"name": "prefers-color-scheme", "value": value})
		}
		if err := p.call(ctx, "Emulation.setEmulatedMedia", map[string]any{"features": features}, nil); err != nil {
			return err
		}
		e.theme = value
		return nil
	}
	defer func() {
		restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := set(restore, previous); err != nil {
			note += "; could not restore the previous theme preference: " + err.Error()
		}
	}()
	for _, theme := range []string{"light", "dark"} {
		var before struct{ Background, Theme, Preference string }
		if err := p.evaluate(ctx, themeStateJS, &before); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		if err := set(ctx, theme); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		// A view transition can defer the DOM update beyond the next two frames.
		wait := themePaintJS(before.Background, before.Theme)
		if before.Preference == theme && (before.Theme == "" || before.Theme == theme) {
			wait = `new Promise(resolve => requestAnimationFrame(() => resolve(true)))`
		}
		var painted bool
		if err := p.evaluate(ctx, wait, &painted); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		var result struct{ Data string }
		if err := p.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true}, &result); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		path := filepath.Join(e.RunDir, "receipts", id+"-"+theme+".png")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		err = opaquePNG(path, func() (color.Color, error) {
			var values []uint8
			if err := p.evaluate(ctx, windowBackgroundJS, &values); err != nil {
				return nil, err
			}
			return rgbaBackground(values)
		})
		shots = append(shots, ThemeShot{Theme: theme, Path: path})
		if err != nil {
			return shots, "theme pair not taken: " + err.Error()
		}
		if !painted {
			// Keep one picture of the pinned page without claiming a second theme.
			if len(shots) > 1 {
				_ = os.Remove(shots[1].Path)
			}
			return shots[:1], themePairIgnored
		}
	}
	if samePixels(shots[0].Path, shots[1].Path) {
		_ = os.Remove(shots[1].Path)
		return shots[:1], themePairIgnored
	}
	return shots, ""
}

func samePixels(a, b string) bool {
	read := func(path string) image.Image {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		img, _ := png.Decode(f)
		return img
	}
	x, y := read(a), read(b)
	if x == nil || y == nil || x.Bounds() != y.Bounds() {
		return false
	}
	for j := x.Bounds().Min.Y; j < x.Bounds().Max.Y; j++ {
		for i := x.Bounds().Min.X; i < x.Bounds().Max.X; i++ {
			r, g, b, a := x.At(i, j).RGBA()
			rr, gg, bb, aa := y.At(i, j).RGBA()
			if r != rr || g != gg || b != bb || a != aa {
				return false
			}
		}
	}
	return true
}
