package litmus

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBugCropKeepsContextAndScalesCSSCoordinates(t *testing.T) {
	bounds := image.Rect(0, 0, 1320, 780)
	box := cropBox{x: 1220, y: 690, w: 50, h: 30, vw: 1320, vh: 780}
	got := cropRect(bounds, box)
	if got.Dx() != 600 || got.Dy() != 400 || !got.In(bounds) || !image.Pt(1245, 705).In(got) {
		t.Fatalf("crop %v does not keep the target and 600x400 context", got)
	}
	box.x, box.y, box.w, box.h, box.vw, box.vh = 610, 345, 25, 15, 660, 390
	if scaled := cropRect(bounds, box); scaled != got {
		t.Fatalf("CSS coordinates yielded %v, want %v", scaled, got)
	}
}

func TestBugImageUsesSelectedReceiptFromAnotherHeadCheck(t *testing.T) {
	dir := t.TempDir()
	pattern := image.NewRGBA(image.Rect(0, 0, 1000, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 1000; x++ {
			pattern.SetRGBA(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 251), A: 255})
		}
	}
	head, later, base := filepath.Join(dir, "11-after.png"), filepath.Join(dir, "44-after.png"), filepath.Join(dir, "base.png")
	if err := os.WriteFile(head, pngBytes(pattern), 0o600); err != nil {
		t.Fatal(err)
	}
	whitePNG(t, later, 1000, 700)
	whitePNG(t, base, 1000, 700)
	earlier := Receipt{Seq: 11, Leg: "head", AfterPath: head, Box: &ElementBox{X: 850, Y: 550, W: 80, H: 60, ViewportWidth: 1000, ViewportHeight: 700}}
	check := Check{ID: "c9", Shot: 11, Receipts: []Receipt{
		{Seq: 42, Leg: "head", AfterPath: later},
		{Seq: 43, Leg: "head", AfterPath: later},
		{Seq: 44, Leg: "head", AfterPath: later},
	}}
	checks := []Check{{ID: "c2", Leg: "head", Receipts: []Receipt{earlier}}, check}
	baseCheck := &Check{Leg: "base", Receipts: []Receipt{{Leg: "base", AfterPath: base}}}
	for _, tc := range []struct {
		name  string
		check Check
	}{
		{"selected", check},
		{"last", Check{Receipts: []Receipt{earlier}}},
	} {
		if err := makeBugImage(bugComment{Check: tc.check, Base: baseCheck}, checks, filepath.Join(dir, tc.name+".png")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := openEvidenceImage(filepath.Join(dir, "selected.png"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := openEvidenceImage(filepath.Join(dir, "last.png"))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 400; y++ {
		for x := 600; x < 1200; x++ {
			if got.At(x, y) != want.At(x, y) {
				t.Fatalf("selected receipt crop differs at %d,%d", x, y)
			}
		}
	}
}

func TestBugImagePrefersCheckEndThemeShots(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, w, h int, fill, corner color.RGBA) string {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), image.NewUniform(fill), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(0, 0, 40, 40), image.NewUniform(corner), image.Point{}, draw.Src)
		// Interior markers also distinguish crops from a solid-color full frame.
		draw.Draw(img, image.Rect(w*7/10, h*7/10, w*7/10+40, h*7/10+40), image.NewUniform(corner), image.Point{}, draw.Src)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pngBytes(img), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	red, blue := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	green, yellow := color.RGBA{G: 255, A: 255}, color.RGBA{R: 255, G: 255, A: 255}
	headAfter := write("102-after.png", 1200, 800, green, yellow)
	baseAfter := write("103-after.png", 1200, 800, yellow, green)
	headLight := write("c13-light.png", 1200, 800, red, blue)
	baseLight := write("c14-light.png", 1200, 800, blue, red)
	headDark := write("c13-dark.png", 1200, 800, green, red)
	baseDark := write("c14-dark.png", 1200, 800, yellow, blue)
	baseLarge := write("c14-large.png", 2400, 1600, blue, red)
	missing := filepath.Join(dir, "missing.png")
	box := &ElementBox{X: 800, Y: 500, W: 80, H: 60}
	cropped := image.Rect(540, 330, 1140, 730)
	for _, tc := range []struct {
		name                   string
		headThemes, baseThemes []ThemeShot
		shot                   int
		cssBox, ownBase        bool
		wantHead, wantBase     string
		headCrop, baseCrop     image.Rectangle
	}{
		{
			name:       "light full frame",
			headThemes: []ThemeShot{{Theme: "dark", Path: headDark}, {Theme: "light", Path: headLight}},
			baseThemes: []ThemeShot{{Theme: "dark", Path: baseDark}, {Theme: "light", Path: baseLight}},
			wantHead:   headLight, wantBase: baseLight,
		},
		{
			name:       "identical pair kept once",
			headThemes: []ThemeShot{{Theme: "light", Path: headLight}},
			baseThemes: []ThemeShot{{Theme: "light", Path: baseLight}},
			wantHead:   headLight, wantBase: baseLight,
		},
		{
			name:       "missing light uses dark",
			headThemes: []ThemeShot{{Theme: "light", Path: missing}, {Theme: "dark", Path: headDark}},
			baseThemes: []ThemeShot{{Theme: "light", Path: missing}, {Theme: "dark", Path: baseDark}},
			wantHead:   headDark, wantBase: baseDark,
		},
		{
			name:     "last receipt without themes uses cropped after",
			wantHead: headAfter, wantBase: baseAfter, headCrop: cropped, baseCrop: cropped,
		},
		{
			name:       "explicit shot overrides theme and crops base theme",
			headThemes: []ThemeShot{{Theme: "light", Path: headLight}},
			baseThemes: []ThemeShot{{Theme: "light", Path: baseLight}}, shot: 11,
			wantHead: headAfter, wantBase: baseLight, headCrop: cropped, baseCrop: cropped,
		},
		{
			name:       "explicit shot scales base theme box",
			baseThemes: []ThemeShot{{Theme: "light", Path: baseLarge}}, shot: 11,
			wantHead: headAfter, wantBase: baseLarge, headCrop: cropped, baseCrop: image.Rect(1380, 860, 1980, 1260),
		},
		{
			name:       "explicit shot scales CSS box",
			baseThemes: []ThemeShot{{Theme: "light", Path: baseLarge}}, shot: 11, cssBox: true,
			wantHead: headAfter, wantBase: baseLarge, headCrop: cropped, baseCrop: image.Rect(1380, 860, 1980, 1260),
		},
		{
			name:       "base falls back to own receipts",
			headThemes: []ThemeShot{{Theme: "light", Path: headLight}},
			baseThemes: []ThemeShot{{Theme: "light", Path: baseLight}}, ownBase: true,
			wantHead: headLight, wantBase: baseLight,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectedBox := *box
			if tc.cssBox {
				selectedBox = ElementBox{X: 400, Y: 250, W: 40, H: 30, ViewportWidth: 600, ViewportHeight: 400}
			}
			head := Check{ID: "c13", Leg: "head", State: CheckFail, Shot: tc.shot, Receipts: []Receipt{
				{Seq: 101, Leg: "head", AfterPath: headAfter, ThemeShots: []ThemeShot{{Theme: "light", Path: headLight}}},
				{Seq: 102, Leg: "head", AfterPath: headAfter, Box: box, ThemeShots: tc.headThemes},
			}}
			base := Check{ID: "c14", Leg: "base", State: CheckPass, Receipts: []Receipt{
				{Seq: 103, Leg: "base", AfterPath: baseAfter, Box: box, ThemeShots: tc.baseThemes},
			}}
			checks := []Check{{ID: "c2", Receipts: []Receipt{{Seq: 11, AfterPath: headAfter, Box: &selectedBox}}}, head, base}
			bug := bugComment{Check: head, Base: &base}
			if tc.ownBase {
				bug.Check.Receipts = append(bug.Check.Receipts, base.Receipts...)
				bug.Base = &Check{Leg: "base"}
			}
			out := filepath.Join(t.TempDir(), "bug.png")
			if err := makeBugImage(bug, checks, out); err != nil {
				t.Fatal(err)
			}
			got, err := openEvidenceImage(out)
			if err != nil {
				t.Fatal(err)
			}
			for side, expected := range []struct {
				path string
				crop image.Rectangle
			}{{tc.wantBase, tc.baseCrop}, {tc.wantHead, tc.headCrop}} {
				want, err := openEvidenceImage(expected.path)
				if err != nil {
					t.Fatal(err)
				}
				if !expected.crop.Empty() {
					want = want.(interface {
						SubImage(image.Rectangle) image.Image
					}).SubImage(expected.crop)
				}
				want = scaleEvidenceImage(want, 600, 400)
				for y := 0; y < 400; y++ {
					for x := 0; x < 600; x++ {
						if color.RGBAModel.Convert(got.At(side*600+x, y)) != want.At(x, y) {
							t.Fatalf("side %d differs at %d,%d: got %v, want %v", side, x, y, got.At(side*600+x, y), want.At(x, y))
						}
					}
				}
			}
		})
	}
}

func TestBugComparisonKeepsBaseLeftAndDoesNotInventMissingEvidence(t *testing.T) {
	dir := t.TempDir()
	base, head, out := filepath.Join(dir, "base.png"), filepath.Join(dir, "head.png"), filepath.Join(dir, "bug.png")
	writeImage := func(path string, c color.RGBA) {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 800, 600))
		for y := 0; y < 600; y++ {
			for x := 0; x < 800; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, img)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	red, blue := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	writeImage(base, red)
	writeImage(head, blue)
	b := bugComment{Check: Check{Receipts: []Receipt{{AfterPath: head}}}, Base: &Check{Receipts: []Receipt{{Leg: "base", AfterPath: base}}}}
	if err := makeBugImage(b, nil, out); err != nil {
		t.Fatal(err)
	}
	if color.RGBAModel.Convert(pixelAt(t, out, 300, 200)) != red || color.RGBAModel.Convert(pixelAt(t, out, 900, 200)) != blue {
		t.Fatal("comparison did not put base left and head right")
	}
	b.Base = nil
	missing := filepath.Join(dir, "missing.png")
	if err := makeBugImage(b, nil, missing); err == nil {
		t.Fatal("missing base shot accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing base created a comparison: %v", err)
	}
}
