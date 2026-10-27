import { describe, expect, it, vi } from "vitest";
import { inlineArtifactPage as inline, MAX_INLINED_IMAGE_BYTES, resolveArtifactReference, type ArtifactAssetLoader } from "./artifactPageInliner";

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
		const load = loader({}, { "huge.png": MAX_INLINED_IMAGE_BYTES + 1 });
		const page = await inlineArtifactPage('<img src="huge.png">', "index.html", load);
		expect(page.html).toBe('<img src="huge.png">');
		expect(page.skipped).toEqual(["huge.png"]);
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
