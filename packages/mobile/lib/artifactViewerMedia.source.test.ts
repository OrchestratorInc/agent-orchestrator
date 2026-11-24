import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const viewer = readFileSync(new URL("../app/artifact/[sessionId].tsx", import.meta.url), "utf8");

function between(text: string, start: string, end: string): string {
	const from = text.indexOf(start);
	expect(from, start).toBeGreaterThanOrEqual(0);
	const to = text.indexOf(end, from + start.length);
	expect(to, end).toBeGreaterThan(from);
	return text.slice(from, to);
}

// The viewer's HTML pages play media inline, and a neighbour the pager keeps
// mounted off screen holds it through the gate, without a reload.
describe("artifact viewer media wiring", () => {
	const web = between(viewer, "function ArtifactWeb(", "function ArtifactImage(");
	const body = between(viewer, "function ArtifactBody(", "function NoPreview(");

	it("tells an HTML page whether it is the page on screen", () => {
		expect(body).toContain("<ArtifactWeb backend={backend} file={file} version={artifact.updatedAt} page={file} active={active} />");
	});

	it("writes the gate for the page as it is now, into every frame, only for an HTML page", () => {
		expect(web).toContain("const mediaGate = pageView ? artifactMediaGateScript(active) : undefined;");
		expect(web).toContain("{...(pageView ? ARTIFACT_MEDIA_PLAYBACK : undefined)}");
		expect(web).toContain("injectedJavaScriptBeforeContentLoaded={mediaGate}");
		expect(web).toContain("injectedJavaScriptBeforeContentLoadedForMainFrameOnly={!pageView}");
	});

	it("posts a change to a loaded page, and once more when it finishes loading", () => {
		const effect = between(web, "useEffect(() => {\n\t\tif (pageView)", "}, [active, pageView]);");
		expect(effect).toContain("web.current?.injectJavaScript(artifactMediaActiveScript(active));");
		const loadEnd = between(web, "onLoadEnd={() => {", "}}");
		expect(loadEnd).toContain("if (pageView) web.current?.injectJavaScript(artifactMediaActiveScript(active));");
	});

	it("never remounts the page for its media: the key is the file's version, how it loads, and Retry", () => {
		expect(web).toContain("key={`${version}|${inlined.html ? \"inline\" : \"url\"}|${attempt}`}");
	});
});
