import { describe, expect, it } from "vitest";
import { bundleDirectory, deliverableArtifacts, type ArtifactFacts } from "../../../../packages/shared/artifacts/deliverables";

function file(path: string, overrides: Partial<ArtifactFacts> = {}): ArtifactFacts {
	return {
		path,
		name: path.split("/").pop() ?? path,
		kind: path.endsWith(".html") ? "html" : path.endsWith(".md") ? "markdown" : "file",
		size: 1,
		updatedAt: "2026-11-01T00:00:00Z",
		...overrides,
	};
}

const paths = (files: { path: string }[]) => files.map((entry) => entry.path);

describe("deliverableArtifacts", () => {
	it("lists only registered files when any file is registered", () => {
		const files = [file("brand.html", { registered: true }), file("draft.md"), file("assets/a.png")];
		expect(paths(deliverableArtifacts(files))).toEqual(["brand.html"]);
	});

	it("falls back to root files when nothing is registered", () => {
		const files = [file("brand.html"), file("notes.md"), file("assets/a.png"), file("assets/b.png")];
		expect(paths(deliverableArtifacts(files))).toEqual(["brand.html", "notes.md"]);
	});

	it("lists everything when nothing is registered and nothing is at the root", () => {
		const files = [file("site/index.html"), file("site/a.png")];
		expect(paths(deliverableArtifacts(files))).toEqual(["site/index.html", "site/a.png"]);
	});

	it("lists one page with 50 linked images as one artifact", () => {
		const images = Array.from({ length: 50 }, (_, index) => file(`assets/${index}.png`));
		expect(paths(deliverableArtifacts([file("brand.html", { registered: true }), ...images]))).toEqual(["brand.html"]);
	});

	it("keeps a nested registered file and hides root files it was not registered with", () => {
		const files = [file("reports/q3.md", { registered: true }), file("stray.png")];
		expect(paths(deliverableArtifacts(files))).toEqual(["reports/q3.md"]);
	});

	it("keeps an older control plane's list working: no field means the fallback", () => {
		const files = [{ ...file("a.md"), registered: undefined }, file("b/c.md")];
		expect(paths(deliverableArtifacts(files))).toEqual(["a.md"]);
	});

	it("returns nothing for no files", () => {
		expect(deliverableArtifacts([])).toEqual([]);
	});
});

describe("bundles", () => {
	const bundle = [
		file("app.icon/icon.json", { size: 2, updatedAt: "2026-11-01T00:00:00Z" }),
		file("app.icon/Assets/mark.png", { size: 3, updatedAt: "2026-11-03T00:00:00Z" }),
		file("app.icon/Assets/mark.svg", { size: 4, updatedAt: "2026-11-02T00:00:00Z" }),
	];

	it("lists a bundle as one entry, registered or not", () => {
		const [entry, ...rest] = deliverableArtifacts(bundle);
		expect(rest).toEqual([]);
		expect(entry).toMatchObject({
			path: "app.icon/Assets/mark.png",
			name: "app.icon",
			size: 9,
			updatedAt: "2026-11-03T00:00:00Z",
			bundle: { path: "app.icon", fileCount: 3 },
		});
		const registered = bundle.map((member) => ({ ...member, registered: true }));
		expect(deliverableArtifacts(registered)).toHaveLength(1);
	});

	it("treats a root bundle as a root file in the fallback", () => {
		expect(deliverableArtifacts([...bundle, file("assets/a.png")]).map((entry) => entry.name)).toEqual(["app.icon"]);
	});

	it("hides a nested bundle when a root file exists", () => {
		const nested = [file("icons/app.icon/icon.json"), file("readme.md")];
		expect(paths(deliverableArtifacts(nested))).toEqual(["readme.md"]);
	});

	it("registers the bundle when one of its files is registered", () => {
		const files = [file("a.icon/x.png", { registered: true }), file("a.icon/y.png"), file("b.md")];
		expect(deliverableArtifacts(files).map((entry) => entry.name)).toEqual(["a.icon"]);
	});

	it("finds the outermost bundle, case-insensitively, and only among directories", () => {
		expect(bundleDirectory("x/A.ICON/b.bundle/c.png")).toBe("x/A.ICON");
		expect(bundleDirectory("x/photo.icon")).toBeUndefined();
		expect(bundleDirectory("x/y.png")).toBeUndefined();
	});
});
