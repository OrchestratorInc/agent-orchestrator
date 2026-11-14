import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const mocks = vi.hoisted(() => {
	class ArtifactTooLargeError extends Error {}
	return {
		ArtifactTooLargeError,
		downloadArtifact: vi.fn(async () => "file:///cache/artifacts/spec.pdf"),
		getArtifactSize: vi.fn(async () => 1024 as number | undefined),
	};
});

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	StyleSheet: { create: (styles: unknown) => styles },
	View: "View",
}));
vi.mock("react-native-pdf-renderer", () => ({ default: "PdfRendererView" }));
vi.mock("./artifactDownload", () => ({
	ArtifactTooLargeError: mocks.ArtifactTooLargeError,
	discardArtifact: vi.fn(),
	downloadArtifact: mocks.downloadArtifact,
}));
vi.mock("./connectionError", () => ({ userFacingError: (_: unknown, fallback: string) => fallback }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: () => ({}) }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("./useHeaderOverlap", () => ({ useGlassBarInset: () => 0 }));

import { MAX_ARTIFACT_DOWNLOAD_BYTES } from "./artifacts";
import type { ArtifactsBackend } from "./artifactsBackend";
import { ArtifactPdf } from "./ArtifactPdf";

// A daemon backend: it tells sizes; Cloud's does not.
const backend = {
	key: "host.a",
	cloud: false,
	streamSource: () => ({ uri: "http://machine/spec.pdf", headers: {} }),
	size: mocks.getArtifactSize,
} as unknown as ArtifactsBackend;
let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	mocks.downloadArtifact.mockClear();
	mocks.getArtifactSize.mockClear();
});

async function show(props: { active: boolean; size: number }) {
	const element = createElement(ArtifactPdf, { backend, sessionId: "ao-1", artifact: { path: "spec.pdf", updatedAt: "v1", size: props.size }, active: props.active, label: "PDF file", hint: "Open it on the machine." });
	await act(async () => {
		if (renderer) renderer.update(element);
		else renderer = create(element);
	});
	await act(async () => {});
	return renderer!;
}

describe("ArtifactPdf", () => {
	it("does not download a page that is not on screen", async () => {
		await show({ active: false, size: 2048 });
		expect(mocks.downloadArtifact).not.toHaveBeenCalled();
	});

	it("downloads once the page is swiped to, and draws it", async () => {
		await show({ active: false, size: 2048 });
		const root = await show({ active: true, size: 2048 });
		expect(mocks.downloadArtifact).toHaveBeenCalledTimes(1);
		expect((mocks.downloadArtifact.mock.calls[0] as unknown[] | undefined)?.[2]).toEqual({ maxBytes: MAX_ARTIFACT_DOWNLOAD_BYTES });
		expect(root.root.findAll((node) => (node.type as unknown) === "PdfRendererView")).toHaveLength(1);
	});

	it("says a listed file over the limit is too large, without downloading it", async () => {
		const root = await show({ active: true, size: MAX_ARTIFACT_DOWNLOAD_BYTES + 1 });
		expect(mocks.downloadArtifact).not.toHaveBeenCalled();
		expect(mocks.getArtifactSize).not.toHaveBeenCalled();
		expect(root.root.find((node) => (node.type as unknown) === "EmptyState").props.title).toBe("Too large to preview on the phone");
	});

	it("asks an unlisted file's size first", async () => {
		mocks.getArtifactSize.mockResolvedValueOnce(MAX_ARTIFACT_DOWNLOAD_BYTES * 2);
		const root = await show({ active: true, size: 0 });
		expect(mocks.getArtifactSize).toHaveBeenCalledTimes(1);
		expect(mocks.downloadArtifact).not.toHaveBeenCalled();
		expect(root.root.find((node) => (node.type as unknown) === "EmptyState").props.title).toBe("Too large to preview on the phone");
	});

	it("says too large when the download itself turns out over the limit", async () => {
		mocks.downloadArtifact.mockRejectedValueOnce(new mocks.ArtifactTooLargeError());
		const root = await show({ active: true, size: 2048 });
		expect(root.root.find((node) => (node.type as unknown) === "EmptyState").props.title).toBe("Too large to preview on the phone");
	});

	it("asks Cloud for fresh access once when a download is refused, and downloads again with it", async () => {
		const renew = vi.fn();
		const cloud = (root: string) => ({ key: "cloud", cloud: true, streamSource: () => ({ uri: `${root}spec.pdf` }), renew }) as unknown as ArtifactsBackend;
		mocks.downloadArtifact.mockRejectedValueOnce(new Error("403"));
		const element = (backend: ArtifactsBackend) => createElement(ArtifactPdf, { backend, sessionId: "ao-1", artifact: { path: "spec.pdf", updatedAt: "v1", size: 2048 }, active: true, label: "PDF file", hint: "" });
		await act(async () => { renderer = create(element(cloud("https://signed/old/"))); });
		await act(async () => {});
		expect(renew).toHaveBeenCalledTimes(1);
		// Still loading, not failed: the renewed backend is on its way.
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "EmptyState")).toHaveLength(0);
		await act(async () => { renderer!.update(element(cloud("https://signed/new/"))); });
		await act(async () => {});
		expect(mocks.downloadArtifact).toHaveBeenCalledTimes(2);
		expect((mocks.downloadArtifact.mock.calls[1] as unknown[])[0]).toEqual({ uri: "https://signed/new/spec.pdf" });
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "PdfRendererView")).toHaveLength(1);
	});
});
