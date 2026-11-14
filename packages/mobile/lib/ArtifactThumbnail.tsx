import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import { WebView } from "react-native-webview";
import { CARD_PAGE_LIMITS } from "./artifactPageInliner";
import { artifactViewKind, isTextArtifact, looksBinary, type SessionArtifact } from "./artifacts";
import { cachedThumbnailText, MAX_THUMBNAIL_TEXT_BYTES, THUMBNAIL_PAGE_WIDTH, THUMBNAIL_TEXT_CHARS, thumbnailPageHtml, thumbnailScale } from "./artifactThumbnailModel";
import { ChatMarkdown } from "./chat/ChatMarkdown";
import { useRenderTheme } from "./chat/RenderFrame";
import { renderThemeScript, withRenderTheme } from "./chat/renderFrameModel";
import type { ArtifactsBackend } from "./artifactsBackend";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { space } from "./tokens";
import { useInlinedArtifactPage } from "./useInlinedArtifactPage";

// A markdown card lays the document out at twice its width and draws it at half.
const MARKDOWN_SCALE = 0.5;

/** Whether a card can show a peek at the file's contents (images have their own). */
export function hasContentThumbnail(artifact: Pick<SessionArtifact, "path" | "kind" | "size">): boolean {
	const kind = artifactViewKind(artifact);
	// A table or diagram file peeks at its source, as a code file does.
	return kind === "html" || ((kind === "markdown" || kind === "text" || kind === "table" || kind === "diagram") && isTextArtifact(artifact) && artifact.size <= MAX_THUMBNAIL_TEXT_BYTES);
}

/**
 * A peek at an artifact's contents on its gallery card, as the desktop gallery
 * shows it: an HTML page drawn small, a markdown document drawn small, or a
 * text file's opening lines. It loads only while `visible`, shows once it has
 * something to show (the card draws a plain tile until then), calls `onFailed`
 * when it cannot, and takes no touches, so the card's own tap and long-press
 * still work.
 */
export function ArtifactThumbnail({ backend, sessionId, artifact, visible, versionOf, onFailed }: {
	backend: ArtifactsBackend;
	sessionId: string;
	artifact: SessionArtifact;
	visible: boolean;
	/** A listed file's modified time, for the images a markdown card shows. */
	versionOf(path: string): string | undefined;
	/** The preview cannot load; the card shows its icon instead. */
	onFailed(): void;
}) {
	const styles = useThemedStyles(makeStyles);
	const [size, setSize] = useState({ width: 0, height: 0 });
	const onLayout = (event: LayoutChangeEvent) => {
		const { width, height } = event.nativeEvent.layout;
		setSize({ width: Math.round(width), height: Math.round(height) });
	};
	const { width, height } = size;
	const kind = artifactViewKind(artifact);
	return (
		<View pointerEvents="none" style={styles.fill} onLayout={onLayout} importantForAccessibility="no-hide-descendants" accessibilityElementsHidden>
			{width > 0 && kind === "html" ? <PageThumbnail backend={backend} sessionId={sessionId} artifact={artifact} visible={visible} width={width} height={height} onFailed={onFailed} />
				: width > 0 && (kind === "markdown" || kind === "text" || kind === "table" || kind === "diagram") ? <TextThumbnail backend={backend} sessionId={sessionId} artifact={artifact} visible={visible} width={width} markdown={kind === "markdown"} styles={styles} versionOf={versionOf} onFailed={onFailed} />
				: null}
		</View>
	);
}

function PageThumbnail({ backend, sessionId, artifact, visible, width, height, onFailed }: { backend: ArtifactsBackend; sessionId: string; artifact: SessionArtifact; visible: boolean; width: number; height: number; onFailed(): void }) {
	const styles = useThemedStyles(makeStyles);
	const web = useRef<WebView>(null);
	const theme = useRenderTheme();
	const [loaded, setLoaded] = useState(false);
	// Small images only: a dozen cards may load at once, and each is drawn small.
	const inlined = useInlinedArtifactPage({ enabled: visible, backend, sessionId, path: artifact.path, version: artifact.updatedAt, limits: CARD_PAGE_LIMITS });
	// The app's theme is written in, so the card paints in it rather than the
	// system's. Cloud's page has none to take: it is drawn on white, as the
	// desktop draws it.
	const paper = backend.cloud;
	const html = useMemo(() => inlined.html ? (paper ? thumbnailPageHtml(inlined.html) : withRenderTheme(thumbnailPageHtml(inlined.html), theme)) : undefined, [inlined.html, paper, theme]);
	// The page lays out in a 420-point window, drawn scaled down to the card.
	const scale = thumbnailScale(width);
	const frame = { width: THUMBNAIL_PAGE_WIDTH, height: Math.ceil(height / scale), transform: [{ scale }] };
	useEffect(() => setLoaded(false), [html]);
	const failed = inlined.failed;
	useEffect(() => { if (failed) onFailed(); }, [failed, onFailed]);
	// Off screen, the page lets its WebView go; the inlined page stays cached.
	if (!visible || !html) return null;
	return (
		<WebView
			ref={web}
			source={{ html }}
			style={[styles.page, paper && styles.paper, frame, !loaded && styles.hidden]}
			scrollEnabled={false}
			bounces={false}
			overScrollMode="never"
			showsHorizontalScrollIndicator={false}
			showsVerticalScrollIndicator={false}
			textZoom={100}
			setSupportMultipleWindows={false}
			javaScriptCanOpenWindowsAutomatically={false}
			allowsLinkPreview={false}
			// Only the page itself; a card never navigates.
			onShouldStartLoadWithRequest={(request) => request.isTopFrame === false || /^(about:|data:)/.test(request.url)}
			onLoadEnd={() => {
				if (!paper) web.current?.injectJavaScript(renderThemeScript(theme));
				setLoaded(true);
			}}
			onError={onFailed}
		/>
	);
}

function TextThumbnail({ backend, sessionId, artifact, visible, width, markdown, styles, versionOf, onFailed }: { backend: ArtifactsBackend; sessionId: string; artifact: SessionArtifact; visible: boolean; width: number; markdown: boolean; styles: ReturnType<typeof makeStyles>; versionOf(path: string): string | undefined; onFailed(): void }) {
	const key = `${backend.key}|${sessionId}|${artifact.path}|${artifact.updatedAt}`;
	const [text, setText] = useState<{ key: string; value?: string }>();
	useEffect(() => {
		if (!visible || text?.key === key) return;
		let live = true;
		void cachedThumbnailText(key, async () => {
			const body = await backend.text(sessionId, artifact.path);
			return looksBinary(body) ? undefined : body.slice(0, THUMBNAIL_TEXT_CHARS);
		}).then((value) => { if (live) setText({ key, value }); });
		return () => { live = false; };
	}, [artifact.path, backend, key, sessionId, text?.key, visible]);
	const value = text?.key === key ? text.value?.trim() : undefined;
	// Read, and nothing to show: unreadable, binary, or empty.
	const empty = text?.key === key && !value;
	useEffect(() => { if (empty) onFailed(); }, [empty, onFailed]);
	// The document's own images, as the viewer shows them.
	const resolveImage = useCallback((url: string) => backend.markdownImageSource(sessionId, artifact.path, url, versionOf), [artifact.path, backend, sessionId, versionOf]);
	if (!value) return null;
	if (markdown) {
		return (
			<View style={styles.surface}>
				<View style={[styles.markdown, { width: width / MARKDOWN_SCALE }]}>
					<ChatMarkdown text={value} resolveImage={resolveImage} diagrams={false} />
				</View>
			</View>
		);
	}
	return (
		<View style={styles.surface}>
			<Text numberOfLines={16} style={styles.code}>{value}</Text>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	fill: { ...StyleSheet.absoluteFill, overflow: "hidden" },
	page: { position: "absolute", top: 0, left: 0, transformOrigin: "top left", backgroundColor: "transparent" },
	paper: { backgroundColor: "#ffffff" },
	hidden: { opacity: 0 },
	surface: { ...StyleSheet.absoluteFill, overflow: "hidden", backgroundColor: t.bgElevated },
	markdown: { padding: space.lg, transform: [{ scale: MARKDOWN_SCALE }], transformOrigin: "top left" },
	code: { padding: space.sm, fontFamily: t.fontMono, fontSize: 8, lineHeight: 11, color: t.textSecondary },
});
