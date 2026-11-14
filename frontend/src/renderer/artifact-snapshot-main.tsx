/**
 * The page behind cached artifact previews. Main loads it in a hidden window,
 * hands it one artifact with `start`, polls `state`, and asks `encode` for the
 * WebP once the preview is complete. It draws the same ArtifactThumbnail a card
 * does, in a box the snapshot's size, and nothing else of the app.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot, type Root } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import "@xterm/xterm/css/xterm.css";
import "./styles.css";
import { ArtifactThumbnail } from "./components/ArtifactThumbnail";
import { preloadMarkdownFonts } from "./hooks/useMarkdownFontsReady";
import { appI18n } from "./i18n";
import { refreshDaemonStatus } from "./lib/daemon-status";
import { applyDocumentTheme, applyDocumentThemeStyle } from "./lib/theme";
import { isThemeStyle, DEFAULT_THEME_STYLE } from "./lib/theme-palettes";
import type { ArtifactRow } from "./lib/artifacts";
import { ARTIFACT_SNAPSHOT_HEIGHT, ARTIFACT_SNAPSHOT_WIDTH, type ArtifactSnapshotJob } from "../shared/artifact-snapshot";

type State = "loading" | "ready" | "unavailable" | "error";

/** How long the preview must stay still before it counts as drawn; a PDF paints its canvas later than the DOM. */
const QUIET_MS = 500;
const QUIET_PDF_MS = 1_200;

const queryClient = new QueryClient({ defaultOptions: { queries: { networkMode: "always", retry: false, refetchOnWindowFocus: false } } });
let activity = Date.now();
let quietMs = QUIET_MS;
let started = false;
let mounted: { root: Root; observer: MutationObserver } | undefined;
const loadedFrames = new WeakSet<Element>();

function bump() {
	activity = Date.now();
}

function state(): State {
	const root = document.getElementById("root");
	const marker = root?.querySelector<HTMLElement>("[data-preview-state]");
	const reported = marker?.dataset.previewState;
	if (reported === "unavailable") return "unavailable";
	if (reported === "error") return "error";
	if (!started || reported !== "content" || !root) return "loading";
	if (Date.now() - activity < quietMs || document.fonts.status !== "loaded") return "loading";
	for (const image of root.querySelectorAll("img")) if (!image.complete) return "loading";
	for (const video of root.querySelectorAll("video")) if (video.readyState < 2) return "loading";
	// An HTML page can take longer than the quiet period to load; a frame still loading is a blank or half-drawn card.
	for (const frame of root.querySelectorAll("iframe")) if (!loadedFrames.has(frame)) return "loading";
	return "ready";
}

/** Unmounts the preview, so the window sits idle until the next artifact. */
function reset(): void {
	started = false;
	mounted?.observer.disconnect();
	mounted?.root.unmount();
	mounted = undefined;
	queryClient.clear();
}

async function start(job: ArtifactSnapshotJob): Promise<void> {
	reset();
	applyDocumentTheme(job.theme);
	applyDocumentThemeStyle(isThemeStyle(job.styleTheme) ? job.styleTheme : DEFAULT_THEME_STYLE);
	await refreshDaemonStatus();
	await document.fonts.ready;
	const row: ArtifactRow = {
		path: job.path,
		name: job.name,
		kind: job.kind as ArtifactRow["kind"],
		updatedAt: job.updatedAt,
		inlineUrl: job.inlineUrl,
		rawUrl: job.rawUrl,
		previewUrl: job.previewUrl,
		sessionId: job.sessionId,
		sessionTitle: job.sessionTitle,
		cloud: job.cloud as ArtifactRow["cloud"],
	};
	quietMs = /\.pdf$/i.test(job.path) ? QUIET_PDF_MS : QUIET_MS;
	const root = document.getElementById("root") as HTMLElement;
	// Anything that changes the preview, or finishes loading into it, restarts the quiet period.
	const observer = new MutationObserver(bump);
	observer.observe(root, { subtree: true, childList: true, attributes: true, characterData: true });
	bump();
	started = true;
	const reactRoot = createRoot(root);
	mounted = { root: reactRoot, observer };
	reactRoot.render(
		<I18nextProvider i18n={appI18n}>
			<QueryClientProvider client={queryClient}>
				<div className="relative overflow-hidden bg-[var(--solid-muted,var(--muted))]" style={{ width: ARTIFACT_SNAPSHOT_WIDTH, height: ARTIFACT_SNAPSHOT_HEIGHT }}>
					<ArtifactThumbnail row={row} />
				</div>
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

/** The frame as WebP, base64: the page's canvas encodes it, which Electron's own images cannot. */
async function encode(png: string, quality: number): Promise<string> {
	const bytes = Uint8Array.from(atob(png), (char) => char.charCodeAt(0));
	const bitmap = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
	const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
	canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
	bitmap.close();
	const blob = await canvas.convertToBlob({ type: "image/webp", quality });
	if (blob.type !== "image/webp") throw new Error("this browser cannot encode WebP");
	const buffer = new Uint8Array(await blob.arrayBuffer());
	let binary = "";
	for (let offset = 0; offset < buffer.length; offset += 0x8000) binary += String.fromCharCode(...buffer.subarray(offset, offset + 0x8000));
	return btoa(binary);
}

void preloadMarkdownFonts();
// Loads and errors do not bubble, but they do capture; listen once for every artifact.
for (const type of ["load", "loadeddata", "error"]) document.getElementById("root")?.addEventListener(type, bump, true);
document.getElementById("root")?.addEventListener("load", (event) => {
	if (event.target instanceof HTMLIFrameElement) loadedFrames.add(event.target);
}, true);
(window as unknown as { __aoSnapshot: unknown }).__aoSnapshot = { start, state, encode, reset };
