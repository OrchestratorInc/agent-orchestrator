import { describe, expect, it, vi } from "vitest";
import { CARD_PAGE_LIMITS, FULL_PAGE_LIMITS, inlineArtifactPage as inline, resolveArtifactReference, type ArtifactAssetLoader } from "./artifactPageInliner";

const inlineArtifactPage = (html: string, pagePath: string, load: ArtifactAssetLoader) => inline(html, pagePath, load, "ao-1");

function loader(files: Record<string, string>, images: Record<string, number> = {}): ArtifactAssetLoader & { dataUri: ReturnType<typeof vi.fn> } {
	return {
		text: async (path) => files[path],
		dataUri: vi.fn(async (path: string) => path in images ? { uri: `data:image/png;base64,${path}`, bytes: images[path]! } : undefined),
	};
}

describe("inlineArtifactPage", () => {
	it("writes the page's images into it, once per file", async () => {
		const load = loader({}, { "deck/thumbs/a.png": 10 });
		const page = await inlineArtifactPage('<img src="thumbs/a.png" srcset="thumbs/a@2x.png 2x"><img src=\'./thumbs/a.png\'>', "deck/index.html", load);
		expect(page.html).toBe('<img src="data:image/png;base64,deck/thumbs/a.png"><img src=\'data:image/png;base64,deck/thumbs/a.png\'>');
		expect(load.dataUri).toHaveBeenCalledTimes(1);
		expect(page.skipped).toEqual([]);
	});

	it("inlines a stylesheet, resolving its url(...) against the stylesheet's own folder", async () => {
		const load = loader({ "css/site.css": "body{background:url('../img/bg.png')}" }, { "img/bg.png": 10 });
		const page = await inlineArtifactPage('<link rel="stylesheet" href="css/site.css"><p style="background:url(img/bg.png)">x</p>', "index.html", load);
		expect(page.html).toBe('<style>body{background:url(data:image/png;base64,img/bg.png)}</style><p style="background:url(data:image/png;base64,img/bg.png)">x</p>');
	});

	it("inlines a script, keeping its other attributes and its text from closing the tag", async () => {
		const load = loader({ "app.js": 'document.write("</script>")' });
		const page = await inlineArtifactPage('<script type="module" src="app.js"></script>', "index.html", load);
		expect(page.html).toBe('<script type="module" >document.write("<\\/script>")</script>');
	});

	it("leaves web URLs, data URIs and missing files as written, and reports the missing", async () => {
		const load = loader({});
		const html = '<img src="https://example.com/a.png"><img src="data:image/gif;base64,R0"><img src="gone.png"><img src="../outside.png">';
		const page = await inlineArtifactPage(html, "index.html", load);
		expect(page.html).toBe(html);
		expect(page.skipped).toEqual(["gone.png"]);
	});

	it("keeps an image over the render size limit as a reference", async () => {
		const load = loader({}, { "huge.png": FULL_PAGE_LIMITS.imageBytes + 1 });
		const page = await inlineArtifactPage('<img src="huge.png">', "index.html", load);
		expect(page.html).toBe('<img src="huge.png">');
		expect(page.skipped).toEqual(["huge.png"]);
	});

	it("holds a card's page to the card budget, asking the loader not to read a larger image", async () => {
		const load = loader({}, { "logo.png": 20 << 10, "screenshot.png": 2 << 20 });
		const page = await inline('<img src="logo.png"><img src="screenshot.png">', "index.html", load, "ao-1", CARD_PAGE_LIMITS);
		expect(page.html).toBe('<img src="data:image/png;base64,logo.png"><img src="screenshot.png">');
		expect(page.skipped).toEqual(["screenshot.png"]);
		expect(load.dataUri).toHaveBeenCalledWith("screenshot.png", CARD_PAGE_LIMITS.imageBytes);
	});

	it("never rewrites code: inline and linked scripts keep their src and srcset text", async () => {
		const inlineScript = '<script>const srcset = "a.png 1x"; el.innerHTML = `<img src="${url}">`;</script>';
		const load = loader({ "app.js": 'document.body.innerHTML = \'<img src="logo.png" srcset="logo@2x.png 2x">\';' }, { "logo.png": 10 });
		const page = await inlineArtifactPage(`${inlineScript}<script src="app.js"></script><img src="logo.png">`, "index.html", load);
		expect(page.html).toBe(`${inlineScript}<script >document.body.innerHTML = '<img src="logo.png" srcset="logo@2x.png 2x">';</script><img src="data:image/png;base64,logo.png">`);
		expect(load.dataUri).toHaveBeenCalledTimes(1);
	});

	it("loads linked files side by side, and one file once while it loads", async () => {
		const started: string[] = [];
		const finish: Array<() => void> = [];
		const load: ArtifactAssetLoader = {
			text: async () => undefined,
			dataUri: (path) => new Promise((resolve) => {
				started.push(path);
				finish.push(() => resolve({ uri: `data:${path}`, bytes: 1 }));
			}),
		};
		const pending = inline('<img src="a.png"><img src="b.png"><img src="a.png"><img src="c.png">', "index.html", load, "ao-1");
		await new Promise((resolve) => setTimeout(resolve, 0));
		// All three files started before any finished.
		expect(started).toEqual(["a.png", "b.png", "c.png"]);
		finish.forEach((done) => done());
		expect((await pending).html).toBe('<img src="data:a.png"><img src="data:b.png"><img src="data:a.png"><img src="data:c.png">');
	});

	it("leaves an image the loader declined to read as written", async () => {
		const load: ArtifactAssetLoader = { text: async () => undefined, dataUri: async () => ({ uri: "", bytes: 5 << 20 }) };
		const page = await inline('<img src="big.png">', "index.html", load, "ao-1", CARD_PAGE_LIMITS);
		expect(page.html).toBe('<img src="big.png">');
	});
});

describe("resolveArtifactReference", () => {
	it("maps an absolute path into this session's artifact folder back into the folder", () => {
		expect(resolveArtifactReference("index.html", "/Users/me/.ao/data/artifacts/ao-1/shots/a.jpg", "ao-1")).toBe("shots/a.jpg");
		expect(resolveArtifactReference("index.html", "C:\\Users\\me\\.ao\\data\\artifacts\\ao-1\\a.png", "ao-1")).toBe("a.png");
	});

	it("refuses another session's folder, other absolute paths, and escapes", () => {
		expect(resolveArtifactReference("index.html", "/Users/me/.ao/data/artifacts/ao-2/a.jpg", "ao-1")).toBeUndefined();
		expect(resolveArtifactReference("index.html", "/etc/passwd", "ao-1")).toBeUndefined();
		expect(resolveArtifactReference("index.html", "/x/artifacts/ao-1/../../etc/a.png", "ao-1")).toBeUndefined();
		expect(resolveArtifactReference("index.html", "//cdn.example.com/a.png", "ao-1")).toBeUndefined();
	});
});
