package litmus

import (
	"bytes"
	"context"
	"encoding/base64"
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
	page, _ := fakeThemePage(t, true)
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

// fakeThemePage speaks the page CDP protocol. Its body is transparent and its
// native background follows the media preference, or stays pinned to dark.
func fakeThemePage(t *testing.T, pinned bool) (*pageCDP, func() []string) {
	page, preferences, _ := fakeThemePageWithPreference(t, pinned, "")
	return page, preferences
}

func fakeThemePageWithPreference(t *testing.T, pinned bool, initialPreference string) (*pageCDP, func() []string, func() string) {
	t.Helper()
	var preferences []string
	preference := initialPreference
	page := &pageCDP{request: func(_ context.Context, method string, params, out any) error {
		var result any = map[string]any{}
		switch method {
		case "Emulation.setEmulatedMedia":
			features := params.(map[string]any)["features"].([]map[string]string)
			preference = ""
			if len(features) > 0 {
				preference = features[0]["value"]
			}
			preferences = append(preferences, preference)
		case "Runtime.evaluate":
			expression := params.(map[string]any)["expression"].(string)
			var value any = !pinned
			switch expression {
			case themeStateJS:
				theme := "dark"
				if preference == "light" && !pinned {
					theme = "light"
				}
				media := preference
				if media == "" {
					media = "dark"
				}
				value = map[string]string{"theme": theme, "background": theme, "preference": media}
			case windowBackgroundJS:
				value = []uint8{12, 12, 14, 255}
				if preference == "light" && !pinned {
					value = []uint8{251, 251, 251, 255}
				}
			}
			result = map[string]any{"result": map[string]any{"value": value}}
		case "Page.captureScreenshot":
			im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
			im.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
			result = map[string]any{"data": base64.StdEncoding.EncodeToString(pngBytes(im))}
		default:
			t.Fatalf("unexpected CDP method %s", method)
		}
		if out == nil {
			return nil
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	}}
	return page, func() []string { return preferences }, func() string { return preference }
}

func TestCheckEndCapturesOpaqueThemePairAndRestoresSystem(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "follows system", true: "pinned theme"}[pinned], func(t *testing.T) {
			svc, _, _, _ := driveSvc(t)
			page, preferences := fakeThemePage(t, pinned)
			run, _ := svc.store.Get("run_s1")
			is(t, "")(nil, svc.store.Save(run))
			svc.engineFor(run.ID, run.Legs[0]).connectPage = func(context.Context) (*pageCDP, error) { return page, nil }
			is(t, "")(svc.CheckAdd(ctx, "s1", "accounts", "limits readable", ""))
			is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
			is(t, "")(svc.Drive(ctx, "s1", "click", []string{"e1"}))
			check, err := svc.CheckEnd(context.Background(), "s1", "c1", CheckPass, "readable", false, 0)
			is(t, "")(nil, err)
			receipt := check.Receipts[len(check.Receipts)-1]
			want := 2
			if pinned {
				want = 1
			}
			if len(receipt.ThemeShots) != want {
				t.Fatalf("receipt %+v", receipt)
			}
			for _, shot := range receipt.ThemeShots {
				requireOpaque(t, shot.Path)
			}
			if pinned && receipt.ThemeNote != themePairIgnored {
				t.Fatalf("note %q", receipt.ThemeNote)
			}
			if !pinned && (receipt.ThemeNote != "" || samePixels(receipt.ThemeShots[0].Path, receipt.ThemeShots[1].Path)) {
				t.Fatalf("theme pair %+v", receipt)
			}
			wantPreferences := "light,dark,"
			if pinned {
				wantPreferences = "light,"
			}
			if got := strings.Join(preferences(), ","); got != wantPreferences {
				t.Fatalf("preferences %q", got)
			}
			saved, _ := svc.store.Checks(run.ID)
			if len(saved[0].Receipts[0].ThemeShots) != want {
				t.Fatal("pair not persisted on receipt")
			}
			media := &sizedMedia{}
			svc.media = media
			verdict := commentVerdict(saved)
			items, _, err := svc.stageEvidence(ctx, commentRun, verdict, t.TempDir())
			is(t, "")(nil, err)
			for i := range items {
				items[i].url = "https://example.test/" + items[i].file
			}
			body := renderComment(commentRun, verdict, evidence{items: items}, commentRun.CreatedAt)
			if pinned && !strings.Contains(body, themePairIgnored) {
				t.Fatalf("comment omitted note: %s", body)
			}
			for _, shot := range receipt.ThemeShots {
				if !strings.Contains(body, shot.Theme+" system theme preference") || !strings.Contains(body, shot.Theme+"-preference.png") {
					t.Fatalf("comment omitted %s picture: %s", shot.Theme, body)
				}
				staged := false
				for _, call := range media.calls {
					if len(call) > 2 && call[2] == shot.Path {
						staged = true
						if strings.Contains(strings.Join(call, " "), "scale=") {
							t.Fatalf("theme shot was downsized: %v", call)
						}
					}
				}
				if !staged {
					t.Fatalf("theme shot was not staged: %s", shot.Path)
				}
			}
			if !strings.Contains(body, "Full retina size; embedded browser pages are separate targets and never appear in these shots.") {
				t.Fatalf("comment omitted capture limits: %s", body)
			}
		})
	}
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

func TestThemePairRestoresPriorPreferenceAfterCaptureFailure(t *testing.T) {
	for _, prior := range []string{"light", "dark"} {
		t.Run(prior, func(t *testing.T) {
			svc, _, _, _ := driveSvc(t)
			run, _ := svc.store.Get("run_s1")
			e := svc.engineFor(run.ID, run.Legs[0])
			page, preferences, active := fakeThemePageWithPreference(t, false, prior)
			e.theme = prior
			failTheme := "light"
			wantShots := 0
			if prior == "light" {
				failTheme, wantShots = "dark", 1
			}
			request := page.request
			page.request = func(ctx context.Context, method string, params, out any) error {
				if method == "Page.captureScreenshot" && active() == failTheme {
					return errors.New("capture failed")
				}
				return request(ctx, method, params, out)
			}
			e.connectPage = func(context.Context) (*pageCDP, error) { return page, nil }
			shots, note := svc.themePair(ctx, e, "c1")
			if len(shots) != wantShots || !strings.Contains(note, "capture failed") || active() != prior || e.theme != prior {
				t.Fatalf("failure did not restore %s: shots=%v note=%q active=%s", prior, shots, note, active())
			}
			wantPreferences := "light,dark"
			if prior == "light" {
				wantPreferences = "light,dark,light"
			}
			if got := strings.Join(preferences(), ","); got != wantPreferences {
				t.Fatalf("preferences %q, want %q", got, wantPreferences)
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

func TestThemePaintPollsDelayedDOMChangeAndBoundsPinnedWait(t *testing.T) {
	for _, changed := range []string{"background", "theme", "neither"} {
		t.Run(changed, func(t *testing.T) {
			fixture := `let now=0, frames=0;
const root={dataset:{theme:'dark'}}, document={documentElement:root};
const performance={now:()=>now}, getComputedStyle=()=>({backgroundColor:now>=125 && ` + fmt.Sprintf("%q", changed) + `==='background'?'light':'dark'});
const setTimeout=(fn,ms)=>{now+=ms; if(now>=125 && ` + fmt.Sprintf("%q", changed) + `==='theme')root.dataset.theme='light'; fn();};
const requestAnimationFrame=fn=>{frames++; fn();};
` + themePaintJS("dark", "dark") + `.then(painted=>console.log(JSON.stringify({painted,now,frames})));`
			out := runNode(t, fixture)
			var got struct {
				Painted     bool
				Now, Frames int
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if changed == "neither" {
				if got.Painted || got.Now != 1500 || got.Frames != 0 {
					t.Fatalf("pinned wait %s", out)
				}
			} else if !got.Painted || got.Now != 125 || got.Frames != 1 {
				t.Fatalf("delayed paint %s", out)
			}
		})
	}
}

func TestThemePairKeepsSamePixelsBackstop(t *testing.T) {
	svc, _, _, _ := driveSvc(t)
	run, _ := svc.store.Get("run_s1")
	e := svc.engineFor(run.ID, run.Legs[0])
	page, preferences := fakeThemePage(t, false)
	request := page.request
	page.request = func(ctx context.Context, method string, params, out any) error {
		if method == "Page.captureScreenshot" {
			data, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(pngBytes(tinyOpaque()))})
			return json.Unmarshal(data, out)
		}
		return request(ctx, method, params, out)
	}
	e.connectPage = func(context.Context) (*pageCDP, error) { return page, nil }
	shots, note := svc.themePair(ctx, e, "c1")
	if len(shots) != 1 || note != themePairIgnored || fileExists(filepath.Join(e.RunDir, "receipts", "c1-dark.png")) || strings.Join(preferences(), ",") != "light,dark," {
		t.Fatalf("same-pixel backstop: shots=%v note=%q preferences=%v", shots, note, preferences())
	}
}

func TestThemePairRetainsShotAfterFlattenFailure(t *testing.T) {
	svc, _, _, _ := driveSvc(t)
	run, _ := svc.store.Get("run_s1")
	e := svc.engineFor(run.ID, run.Legs[0])
	page, preferences := fakeThemePage(t, false)
	request := page.request
	page.request = func(ctx context.Context, method string, params, out any) error {
		if method == "Runtime.evaluate" && params.(map[string]any)["expression"] == windowBackgroundJS {
			return errors.New("cannot sample window background")
		}
		return request(ctx, method, params, out)
	}
	e.connectPage = func(context.Context) (*pageCDP, error) { return page, nil }
	shots, note := svc.themePair(ctx, e, "c1")
	if len(shots) != 1 || !fileExists(shots[0].Path) || !strings.Contains(note, "cannot sample window background") || strings.Join(preferences(), ",") != "light," {
		t.Fatalf("flatten failure lost theme evidence: shots=%v note=%q preferences=%v", shots, note, preferences())
	}
}

func TestCheckEndSkipsThemePairForSkippedAndUnlaunchedLeg(t *testing.T) {
	for _, unlaunched := range []bool{false, true} {
		t.Run(fmt.Sprintf("unlaunched=%t", unlaunched), func(t *testing.T) {
			svc, f, _, _ := driveSvc(t)
			run, _ := svc.store.Get("run_s1")
			e := svc.engineFor(run.ID, run.Legs[0])
			e.connectPage = func(context.Context) (*pageCDP, error) {
				t.Fatal("skipped check connected to a theme target")
				return nil, nil
			}
			if unlaunched {
				run.Legs[0].State = "prepared"
				is(t, "")(nil, svc.store.Save(run))
			}
			is(t, "")(svc.CheckAdd(ctx, "s1", "unreachable", "screen visible", ""))
			is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
			result := CheckSkipped
			wantReceipts := 0
			if unlaunched {
				checks, err := svc.store.Checks(run.ID)
				is(t, "")(nil, err)
				checks[0].Receipts = []Receipt{{Action: "click", AfterPath: evidenceFile(t)}}
				is(t, "")(nil, svc.store.SaveChecks(run.ID, checks))
				result, wantReceipts = CheckPartial, 1
			}
			check, err := svc.CheckEnd(ctx, "s1", "c1", result, "screen no longer reachable", false, 0)
			is(t, "")(nil, err)
			if len(check.Receipts) != wantReceipts || len(f.calls) != 0 {
				t.Fatalf("skipped check captured evidence: %+v, %v", check, f.calls)
			}
			for _, receipt := range check.Receipts {
				if len(receipt.ThemeShots) != 0 || receipt.ThemeNote != "" {
					t.Fatalf("unlaunched leg captured themes: %+v", receipt)
				}
			}
		})
	}
}

func TestWorthALookNotesStayOutOfVerdict(t *testing.T) {
	checks := []Check{
		{ID: "c1", Name: "accounts", Expect: "limits readable", State: CheckPass, Note: "Limits are readable. Worth a look: Similar rows use different summaries.", Receipts: []Receipt{{Action: "click", AfterPath: evidenceFile(t)}}},
		{ID: "c2", Name: "accounts", Leg: "base", State: CheckFail, Receipts: []Receipt{{Leg: "base", Action: "click", AfterPath: evidenceFile(t)}}},
	}
	v := commentVerdict(checks)
	if v.Overall != CheckPass || len(v.Reasons) != 0 {
		t.Fatalf("UI note changed verdict: %+v", v)
	}
	body := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if strings.Count(body, "Worth a look:") != 1 || !strings.Contains(body, "Similar rows use different summaries.") || !strings.Contains(body, "limits are readable.") {
		t.Fatalf("comment UI note: %s", body)
	}
}

func TestRequiredThemePicturesSurviveCommentProseCap(t *testing.T) {
	var items []evItem
	for i := 0; i < 80; i++ {
		items = append(items, evItem{kind: kindTheme, url: fmt.Sprintf("https://example.test/theme-%d.png", i), alt: "system theme", caption: strings.Repeat("Changed screen theme evidence ", 5)})
	}
	body := renderComment(commentRun, commentVerdict(commentChecks(t)), evidence{items: items}, commentRun.CreatedAt)
	for _, item := range items {
		if !strings.Contains(body, item.url) {
			t.Fatalf("required theme picture dropped: %s", item.url)
		}
	}
}
