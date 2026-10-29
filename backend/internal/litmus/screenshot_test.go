package litmus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func tinyOpaque() image.Image {
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			im.Set(x, y, color.RGBA{A: 255})
		}
	}
	return im
}

func pngBytes(im image.Image) []byte { var b bytes.Buffer; _ = png.Encode(&b, im); return b.Bytes() }

func runNode(t *testing.T, fixture string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for this JavaScript capture test")
	}
	out, err := exec.Command(node, "-e", fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("node fixture: %v: %s", err, out)
	}
	return out
}

func requireOpaque(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for y := im.Bounds().Min.Y; y < im.Bounds().Max.Y; y++ {
		for x := im.Bounds().Min.X; x < im.Bounds().Max.X; x++ {
			if _, _, _, a := im.At(x, y).RGBA(); a != 65535 {
				t.Fatalf("%s pixel %d,%d has alpha %d", path, x, y, a)
			}
		}
	}
	return im
}

func TestSavedReceiptFlattensTransparentBodyOntoWindowBackground(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	im.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	f.shot = pngBytes(im)
	run, _ := svc.store.Get("run_s1")
	page := &pageCDP{request: func(_ context.Context, method string, params, out any) error {
		if method == "Runtime.evaluate" && params.(map[string]any)["expression"] == pageThemeJS {
			return json.Unmarshal([]byte(`{"result":{"value":"dark"}}`), out)
		}
		data := []byte(`{"result":{"value":[12,12,14,255]}}`)
		return json.Unmarshal(data, out)
	}}
	svc.engineFor(run.ID, run.Legs[0]).connectPage = func(context.Context) (*pageCDP, error) { return page, nil }
	f.out["eval"] = "--- AGENT_BROWSER_PAGE_CONTENT nonce=123456 origin=app ---\n[12,12,14,255]\n--- END_AGENT_BROWSER_PAGE_CONTENT nonce=123456 ---"
	res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
	is(t, "")(nil, err)
	rec := res["receipt"].(actionRec)
	for _, path := range []string{rec.Before, rec.After} {
		shot := requireOpaque(t, path)
		r, g, b, _ := shot.At(1, 1).RGBA()
		if r != 12*257 || g != 12*257 || b != 14*257 {
			t.Fatalf("background %d,%d,%d", r, g, b)
		}
	}
	_, err = svc.Drive(ctx, "s1", "screenshot", []string{"transparent.png"})
	is(t, "")(nil, err)
	requireOpaque(t, filepath.Join(runDir, "transparent.png"))
}

func TestWindowBackgroundFallsBackWhenSampleHasAlpha(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			fixture := `const document = {documentElement:{dataset:{theme:` + fmt.Sprintf("%q", theme) + `}}, body:null,
 createElement:()=>({getContext:()=>{let fill='#000000', pixel=[0,0,0,0];
 const pixels=new Map([['#0c0c0e',[12,12,14,255]],['#fbfbfb',[251,251,251,255]],['rgba(20,30,40,0.5)',[20,30,40,128]]]);
 return {get fillStyle(){return fill},set fillStyle(value){if(pixels.has(value))fill=value},clearRect(){pixel=[0,0,0,0]},fillRect(){pixel=pixels.get(fill)},
 getImageData(){return {data:pixel}}};}})};
const getComputedStyle = () => ({getPropertyValue:()=> 'rgba(20,30,40,0.5)'});
console.log(JSON.stringify(` + windowBackgroundJS + `));`
			out := runNode(t, fixture)
			want := "[251,251,251,255]"
			if theme == "dark" {
				want = "[12,12,14,255]"
			}
			if strings.TrimSpace(string(out)) != want {
				t.Fatalf("background %s, want %s", out, want)
			}
		})
	}
}

func TestWindowBackgroundFallsBackWhenCanvasRejectsSample(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			fixture := `const document = {documentElement:{dataset:{theme:` + fmt.Sprintf("%q", theme) + `}}, body:null,
 createElement:()=>({getContext:()=>{let fill='#000000', pixel=[0,0,0,0];
 const pixels=new Map([['#0c0c0e',[12,12,14,255]],['#fbfbfb',[251,251,251,255]]]);
 return {get fillStyle(){return fill},set fillStyle(value){if(pixels.has(value))fill=value},clearRect(){pixel=[0,0,0,0]},fillRect(){pixel=pixels.get(fill)},
 getImageData(){return {data:pixel}}};}})};
const getComputedStyle = () => ({getPropertyValue:()=> 'not a CSS color'});
console.log(JSON.stringify(` + windowBackgroundJS + `));`
			out := runNode(t, fixture)
			want := "[251,251,251,255]"
			if theme == "dark" {
				want = "[12,12,14,255]"
			}
			if strings.TrimSpace(string(out)) != want {
				t.Fatalf("background %s, want %s", out, want)
			}
		})
	}
}

func TestFlattenFailurePreservesShotAndReceipt(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	f.shot = pngBytes(image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	run, _ := svc.store.Get("run_s1")
	svc.engineFor(run.ID, run.Legs[0]).connectPage = func(context.Context) (*pageCDP, error) {
		return nil, errors.New("cannot sample background")
	}
	res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
	is(t, "")(nil, err)
	rec := res["receipt"].(actionRec)
	if rec.Before == "" || rec.After == "" || len(rec.Warnings) == 0 || len(readActions(runDir)) != 1 {
		t.Fatalf("flatten failure lost evidence or warning: %+v", rec)
	}
	for _, path := range []string{rec.Before, rec.After} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, f.shot) {
			t.Fatalf("saved screenshot changed after flatten failure: %s, %v", path, err)
		}
	}
	res, err = svc.Drive(ctx, "s1", "screenshot", []string{"saved.png"})
	is(t, "")(nil, err)
	if res["warning"] == nil || !fileExists(filepath.Join(runDir, "saved.png")) {
		t.Fatalf("explicit screenshot lost after flatten failure: %v", res)
	}
	path := filepath.Join(runDir, "saved.png")
	if err := opaquePNG(path, func() (color.Color, error) { return nil, errors.New("cannot sample background") }); err == nil {
		t.Fatal("flatten error was suppressed")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, f.shot) {
		t.Fatal("original screenshot lost after background sampling failed")
	}
}

func TestReceiptRecordsThemeAndShowsItWithCoverageInVerdict(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			svc, _, _, dir := driveSvc(t)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			svc.engineFor(run.ID, run.Legs[0]).connectPage = func(context.Context) (*pageCDP, error) {
				return &pageCDP{request: func(_ context.Context, method string, params, out any) error {
					if method != "Runtime.evaluate" || params.(map[string]any)["expression"] != pageThemeJS {
						t.Fatalf("unexpected theme request: %s %v", method, params)
					}
					data, _ := json.Marshal(map[string]any{"result": map[string]any{"value": theme}})
					return json.Unmarshal(data, out)
				}}, nil
			}
			res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
			is(t, "")(nil, err)
			rec := res["receipt"].(actionRec)
			actions := readActions(dir)
			receipts, err := svc.store.Receipts(run.ID)
			is(t, "")(nil, err)
			if rec.Theme != theme || len(actions) != 1 || actions[0].Theme != theme || len(receipts) != 1 || receipts[0].Theme != theme {
				t.Fatalf("theme not persisted: rec %+v actions %+v receipts %+v", rec, actions, receipts)
			}
			v := passVerdict()
			_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, "")(nil, err)
			body := readRunFile(t, svc, run, "verdict.md")
			if !strings.Contains(body, "[head receipt 1 (theme="+theme+")](receipts/head-1-after.png)") || !strings.Contains(body, "Opened the menu.") {
				t.Fatalf("verdict.md omits receipt theme cited by coverage: %s", body)
			}
		})
	}
}

func TestReceiptThemePrefersDataThemeThenSystemPreference(t *testing.T) {
	for _, tc := range []struct {
		theme, want string
		dark        bool
	}{{"light", "light", true}, {"dark", "dark", false}, {"", "dark", true}, {"", "light", false}} {
		t.Run(fmt.Sprintf("theme=%s/dark=%t", tc.theme, tc.dark), func(t *testing.T) {
			fixture := fmt.Sprintf(`const document = {documentElement: {dataset: {theme: %q}}};
const matchMedia = query => {if (query !== '(prefers-color-scheme: dark)') throw Error(query); return {matches: %t};};
console.log(%s);`, tc.theme, tc.dark, pageThemeJS)
			if out := strings.TrimSpace(string(runNode(t, fixture))); out != tc.want {
				t.Fatalf("theme %q, want %q", out, tc.want)
			}
		})
	}
}
