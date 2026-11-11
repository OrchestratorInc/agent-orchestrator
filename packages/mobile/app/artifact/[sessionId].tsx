import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import * as Clipboard from "expo-clipboard";
import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AccessibilityInfo, ActivityIndicator, FlatList, Image, Keyboard, Platform, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent, type ViewToken } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { artifactFileUrl, artifactImageSource, artifactMarkdownImageSource, getArtifactText } from "../../lib/api";
import { ArtifactThumbnail, hasContentThumbnail } from "../../lib/ArtifactThumbnail";
import { artifactVisual } from "../../lib/artifactVisual";
import { headerTitleWidth } from "../../lib/pagerTitle";
import { useArtifactActions } from "../../lib/artifact-menu";
import { artifactCommentTarget, artifactLanguage, artifactPageAt, artifactViewKind, canShowArtifactHistory, formatArtifactSize, looksBinary, MAX_ARTIFACT_TEXT_BYTES, shownText, showsInWebView, artifactViewerPages, type SessionArtifact } from "../../lib/artifacts";
import { backOr } from "../../lib/backNavigation";
import { HighlightedCodeText } from "../../lib/chat/HighlightedCodeText";
import { useRenderTheme } from "../../lib/chat/RenderFrame";
import { renderThemeScript, withRenderTheme } from "../../lib/chat/renderFrameModel";
import { ChatMarkdown, type MarkdownImageSource } from "../../lib/chat/ChatMarkdown";
import { authHeaders, type ServerConfig } from "../../lib/config";
import { userFacingError } from "../../lib/connectionError";
import { sendFileFeedback } from "../../lib/files/api";
import { SelectionComposer, type ComposerAction } from "../../lib/files/components";
import { ReviewButton } from "../../lib/files/ReviewButton";
import { addReviewComment, reviewKey } from "../../lib/files/reviewStore";
import { useReviewSender } from "../../lib/files/useReviewSender";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { Feather } from "../../lib/icons";
import { imageSourceForPlatform } from "../../lib/imageSource";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { useGlassBarInset } from "../../lib/useHeaderOverlap";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { font, fontScaleCap, iconSize, radius, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";
import { useInlinedArtifactPage } from "../../lib/useInlinedArtifactPage";
import { useSessionArtifacts } from "../../lib/useSessionArtifacts";

// Above this, a text file shows as plain text: a full grammar pass over it
// would stall the JS thread.
const MAX_HIGHLIGHTED_BYTES = 100 * 1024;

/**
 * One artifact, full screen: the phone's counterpart of the desktop's artifact
 * viewer. Opened from the gallery, a chat's inline artifact, or a worker
 * report's file link. A file the session does not list (the list is capped)
 * still opens, as the page the daemon renders, without the actions menu.
 *
 * Asking the agent works as on desktop: text selected in a markdown or text
 * artifact ("Add to chat" in the selection menu) opens a comment on that text,
 * and Ask the agent comments on the whole file. A comment is sent now or added
 * to the session's review, the same one the Files screen keeps.
 *
 * A listed artifact opens as one page of a gallery: swiping sideways moves
 * through the other artifacts the list shows, in the gallery's order when it opened
 * (a file rewritten or written later never moves the page on screen). The header and
 * its actions follow the page on screen.
 */
export default function ArtifactViewerScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ArtifactViewerContent /></HostScope> : <ArtifactViewerContent />;
}

function ArtifactViewerContent() {
	const glassInset = useGlassBarInset();
	// `ask` opens a comment on the whole file, from the gallery's Ask the agent.
	const { sessionId: rawId, path: rawPath, hostId: routeHostId, ask: askParam } = useLocalSearchParams<{ sessionId: string; path?: string; hostId?: string; ask?: string }>();
	const sessionId = String(rawId ?? "");
	const path = String(rawPath ?? "");
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { artifacts, loading } = useSessionArtifacts(hostMatches ? sessionId : "");
	// A listed file's modified time, so a document's regenerated image is not served from cache.
	const versionOf = useCallback((filePath: string) => artifacts?.find((file) => file.path === filePath)?.updatedAt, [artifacts]);
	// The page on screen: the file the route opened, until a swipe moves on, or
	// a link replaces the route's path (below).
	const [currentPath, setCurrentPath] = useState(path);
	const [openedPath, setOpenedPath] = useState(path);
	// The pages keep the order they had on open: the list is newest first, so a
	// file the agent rewrites would otherwise jump to the front and remount.
	const pageOrder = useRef<string[]>(undefined);
	const pages = useMemo(() => {
		if (!artifacts) return undefined;
		const next = artifactViewerPages(pageOrder.current, artifacts, currentPath);
		pageOrder.current = next.map((page) => page.path);
		return next;
	}, [artifacts, currentPath]);
	const artifact = pages?.find((candidate) => candidate.path === currentPath);
	const pageIndex = artifact && pages ? pages.indexOf(artifact) : -1;
	const pageCount = pages?.length ?? 0;
	const leave = useCallback(() => backOr(router), [router]);
	const { openMenu, menu } = useArtifactActions(sessionId, leave);
	const openHistory = useCallback((target: SessionArtifact) => {
		router.push({ pathname: "/sheets/artifact-history", params: { sessionId, path: target.path, name: target.name, hostId: routeHostId } });
	}, [routeHostId, router, sessionId]);

	// The open comment: on selected text (`quote`), or on the whole file. `key`
	// remounts the composer for a new selection, which clears what was typed.
	const [ask, setAsk] = useState<{ quote?: string; key: number } | null>(null);
	// A selection of only whitespace (the gap between paragraphs) quotes nothing.
	const askAbout = useCallback((quote?: string) => setAsk((current) => ({ quote: quote?.trim() || undefined, key: (current?.key ?? 0) + 1 })), []);
	// Android keeps the keyboard up after its input unmounts, over the review tray.
	const closeAsk = useCallback(() => {
		Keyboard.dismiss();
		setAsk(null);
	}, []);
	const showPage = useCallback((next: SessionArtifact) => {
		closeAsk();
		setActionError(undefined);
		setCurrentPath(next.path);
	}, [closeAsk]);
	const pagerRef = useRef<PagerHandle>(null);
	// A tap on the strip moves the pager there with an animation, then shows that page.
	const pageTo = useCallback((target: number) => {
		const next = pages?.[target];
		if (!next) return;
		pagerRef.current?.scrollToPage(target);
		showPage(next);
	}, [pages, showPage]);
	const askedOnOpen = useRef(false);
	useEffect(() => {
		if (askParam !== "1" || askedOnOpen.current || !artifact) return;
		askedOnOpen.current = true;
		askAbout();
	}, [artifact, askAbout, askParam]);
	const reviewSessionKey = reviewKey(currentHostId ?? undefined, sessionId);
	const review = useReviewSender(config, reviewSessionKey, sessionId);
	const [busy, setBusy] = useState(false);
	const [actionError, setActionError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	// A link to another file while this is open replaces the route's path. The
	// viewer goes to that file and, as a swipe does, closes a comment opened on
	// the previous one, so it is never sent against the new file.
	if (openedPath !== path) {
		setOpenedPath(path);
		setCurrentPath(path);
		setAsk(null);
		setActionError(undefined);
	}
	// Android keeps the keyboard up after the composer's input unmounts.
	const routedPath = useRef(path);
	useEffect(() => {
		if (routedPath.current === path) return;
		routedPath.current = path;
		Keyboard.dismiss();
	}, [path]);
	useEffect(() => {
		if (!notice) return;
		AccessibilityInfo.announceForAccessibility(notice);
		const timer = setTimeout(() => setNotice(undefined), 3_000);
		return () => clearTimeout(timer);
	}, [notice]);

	const act = useCallback(async (action: ComposerAction, text: string) => {
		if (!config || !artifact || !ask) return;
		const feedback = text.trim();
		const target = artifactCommentTarget(artifact, ask.quote);
		const label = `${artifact.name} · ${ask.quote ? "Selected text" : "Whole file"}`;
		if (action === "comment") {
			addReviewComment(reviewSessionKey, { label, feedback, target });
			haptics.success();
			closeAsk();
			setNotice("Comment added to the review.");
			return;
		}
		setBusy(true);
		setActionError(undefined);
		try {
			await sendFileFeedback(config, sessionId, [{ target, feedback }]);
			addReviewComment(reviewSessionKey, { label, feedback, target, status: "sent" });
			haptics.success();
			closeAsk();
			setNotice("Sent. The agent's reply appears in the session.");
		} catch (cause) {
			haptics.error();
			setActionError(userFacingError(cause, "Couldn't send that to the agent."));
		} finally {
			setBusy(false);
		}
	}, [artifact, ask, closeAsk, config, reviewSessionKey, sessionId]);

	// The toolbar beside the title: ⋯, and version history where the file has one.
	const headerActions = artifact && canShowArtifactHistory(artifact) ? 2 : 1;
	useLayoutEffect(() => {
		const title = artifact?.name ?? path.split("/").pop() ?? "Artifact";
		navigation.setOptions({
			title,
			// In a gallery of several, the title also says where this page sits.
			headerTitle: pageCount > 1 && pageIndex >= 0 ? () => <PagerTitle title={title} position={`${pageIndex + 1} of ${pageCount}`} actions={headerActions} /> : undefined,
		});
	}, [artifact?.name, headerActions, navigation, pageCount, pageIndex, path]);
	// Memoized: each new element re-registers the header's items.
	const headerItems = useMemo(() => artifact ? (
		<Stack.Toolbar placement="right">
			{canShowArtifactHistory(artifact) ? <Stack.Toolbar.Button icon={toolbarIcon("history")} accessibilityLabel="Version history" onPress={() => openHistory(artifact)} /> : null}
			<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Artifact actions" onPress={() => openMenu(artifact, { inViewer: true, onAsk: () => askAbout(), onHistory: () => openHistory(artifact) })} />
		</Stack.Toolbar>
	) : null, [artifact, askAbout, openHistory, openMenu]);
	const openReview = useCallback(() => {
		router.push({ pathname: "/files/[sessionId]/review", params: { sessionId, hostId: routeHostId } });
	}, [routeHostId, router, sessionId]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon={{ ios: "doc.text", android: "description" }} title="This artifact belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon={{ ios: "square.grid.2x2", android: "dashboard" }} onPress={() => router.navigate("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="The artifact loads once the app reconnects." /></View>;
	if (!config || (!artifacts && loading)) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!artifact || !pages) {
		const url = artifactFileUrl(config, sessionId, currentPath);
		return url ? <ArtifactWeb config={config} url={url} version="" page={/\.html?$/i.test(currentPath) ? { sessionId, path: currentPath } : undefined} /> : <Unavailable title="This artifact can't be opened" message="Its path isn't one the phone can request." />;
	}
	// The strip shows the session's artifacts when there is more than one. It leaves while a comment is open.
	const filmstrip = !ask && pages.length > 1;
	return (
		<View style={styles.screen}>
			{headerItems}
			<ArtifactPager
				ref={pagerRef}
				pages={pages}
				index={pageIndex}
				// A comment belongs to the file it was opened on; paging waits until it is sent or closed.
				scrollEnabled={!ask}
				onPage={showPage}
				renderPage={(page) => <ArtifactBody config={config} sessionId={sessionId} artifact={page} versionOf={versionOf} onAsk={page.path === currentPath ? askAbout : () => {}} />}
			/>
			{filmstrip ? <ArtifactFilmstrip config={config} sessionId={sessionId} artifacts={pages} index={pageIndex} versionOf={versionOf} onSelect={pageTo} /> : null}
			{notice ? <View pointerEvents="none" style={[styles.notice, { top: glassInset + space.md }]}><Text style={styles.noticeText}>{notice}</Text></View> : null}
			{ask ? (
				<SelectionComposer
					key={ask.key}
					label={`${artifact.name} · ${ask.quote ? "Selected text" : "Whole file"}`}
					busy={busy}
					error={actionError}
					onAction={(action, text) => void act(action, text)}
					onCopy={ask.quote ? () => void Clipboard.setStringAsync(ask.quote ?? "") : undefined}
					onClose={() => { closeAsk(); setActionError(undefined); }}
				/>
			) : (
				<ReviewButton count={review.comments.length} bottomOffset={filmstrip ? STRIP_HEIGHT + space.sm : 0} onPress={openReview} />
			)}
			{menu}
		</View>
	);
}

/**
 * The session's artifacts side by side, one page each, swiped like a photo
 * gallery. Only the page on screen and its neighbours are mounted, so a long
 * list does not load every web page at once.
 */
type PagerHandle = { scrollToPage(index: number): void };

const ArtifactPager = forwardRef<PagerHandle, {
	pages: readonly SessionArtifact[];
	index: number;
	scrollEnabled: boolean;
	onPage(artifact: SessionArtifact): void;
	renderPage(artifact: SessionArtifact): ReactNode;
}>(function ArtifactPager({ pages, index, scrollEnabled, onPage, renderPage }, handle) {
	const styles = useThemedStyles(makeStyles);
	const list = useRef<FlatList<SessionArtifact>>(null);
	// The page the list is on. A move the list made itself (a swipe, a tap on the
	// strip) already has it here, so the effects below do not scroll again.
	const shownIndex = useRef(index);
	// Pages are as wide as the viewer, which on an iPad in split view is not the screen.
	const [size, setSize] = useState<{ width: number; height: number }>();
	const onLayout = useCallback((event: LayoutChangeEvent) => {
		const { width, height } = event.nativeEvent.layout;
		setSize((current) => current?.width === width && current.height === height ? current : { width, height });
	}, []);
	const width = size?.width ?? 0;
	useImperativeHandle(handle, () => ({
		scrollToPage(target) {
			shownIndex.current = target;
			list.current?.scrollToIndex({ index: target, animated: true });
		},
	}), []);
	// Pages keep their order, so the index only moves when an earlier page is
	// removed. The list follows before paint, so the page on screen stays put.
	useLayoutEffect(() => {
		if (width <= 0 || index < 0 || index === shownIndex.current) return;
		shownIndex.current = index;
		list.current?.scrollToOffset({ offset: index * width, animated: false });
	}, [index, width]);
	// A resize changes the page width, so the page on screen is put back at its width.
	useEffect(() => {
		if (width > 0) list.current?.scrollToOffset({ offset: shownIndex.current * width, animated: false });
	}, [width]);
	const onSettled = useCallback((event: NativeSyntheticEvent<NativeScrollEvent>) => {
		const next = artifactPageAt(pages, event.nativeEvent.contentOffset.x, width);
		if (!next || next === pages[index]) return;
		shownIndex.current = pages.indexOf(next);
		onPage(next);
	}, [index, onPage, pages, width]);
	return (
		<View style={styles.screen} onLayout={onLayout}>
			{size ? (
				<FlatList
					ref={list}
					data={pages}
					horizontal
					pagingEnabled
					scrollEnabled={scrollEnabled && pages.length > 1}
					showsHorizontalScrollIndicator={false}
					keyExtractor={(artifact) => artifact.path}
					getItemLayout={(_, item) => ({ length: size.width, offset: size.width * item, index: item })}
					initialScrollIndex={Math.max(0, index)}
					initialNumToRender={1}
					maxToRenderPerBatch={1}
					windowSize={3}
					onMomentumScrollEnd={onSettled}
					renderItem={({ item }) => <View style={{ width: size.width, height: size.height }}>{renderPage(item)}</View>}
				/>
			) : null}
		</View>
	);
});

const STRIP_TILE = 56;
const STRIP_GAP = space.xs;
const STRIP_PAD = space.sm;
/** The strip's height without the home indicator's inset. */
const STRIP_HEIGHT = STRIP_TILE + STRIP_PAD * 2;

/**
 * Every artifact of the session as a row of small tiles, like a photo app's
 * strip: the page on screen is outlined and kept in the middle of the row as
 * the pages change. Tapping a tile goes to that page.
 */
function ArtifactFilmstrip({ config, sessionId, artifacts, index, versionOf, onSelect }: {
	config: ServerConfig;
	sessionId: string;
	artifacts: readonly SessionArtifact[];
	index: number;
	versionOf(path: string): string | undefined;
	onSelect(index: number): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const list = useRef<FlatList<SessionArtifact>>(null);
	// Tiles that are on screen may load their preview; the others let theirs go.
	const [visiblePaths, setVisiblePaths] = useState<ReadonlySet<string>>(() => new Set());
	const onViewableItemsChanged = useRef(({ viewableItems }: { viewableItems: ViewToken<SessionArtifact>[] }) => {
		setVisiblePaths(new Set(viewableItems.flatMap((token) => token.item ? [token.item.path] : [])));
	}).current;
	const viewabilityConfig = useRef({ itemVisiblePercentThreshold: 50, minimumViewTime: 120 }).current;
	// A picture or preview that cannot load falls back to the kind's glyph, until the file changes.
	const [previewFailed, setPreviewFailed] = useState<Readonly<Record<string, string>>>({});
	// Returns the same object when nothing changed, so a preview reporting failure again does not re-render.
	const markPreviewFailed = useCallback((path: string, updatedAt: string) => {
		setPreviewFailed((previous) => previous[path] === updatedAt ? previous : { ...previous, [path]: updatedAt });
	}, []);
	// The first placement happens at once; later ones glide.
	const placed = useRef(false);
	useEffect(() => {
		if (index < 0) return;
		list.current?.scrollToIndex({ index, viewPosition: 0.5, animated: placed.current });
		placed.current = true;
	}, [index]);
	return (
		<View style={[styles.strip, { paddingBottom: insets.bottom }]}>
			<FlatList
				ref={list}
				style={styles.stripList}
				contentContainerStyle={styles.stripContent}
				data={artifacts}
				horizontal
				showsHorizontalScrollIndicator={false}
				onViewableItemsChanged={onViewableItemsChanged}
				viewabilityConfig={viewabilityConfig}
				keyExtractor={(artifact) => artifact.path}
				// Fixed-size tiles let the list jump to any of them without measuring.
				getItemLayout={(_, item) => ({ length: STRIP_TILE + STRIP_GAP, offset: (STRIP_TILE + STRIP_GAP) * item, index: item })}
				// No initialScrollIndex: when the tiles fit without scrolling, the list
				// would render from that tile on and never draw (or preview) the ones before
				// it. The effect above places the strip instead.
				renderItem={({ item, index: position }) => {
					const selected = position === index;
					const visual = artifactVisual(item, t);
					const failed = previewFailed[item.path] === item.updatedAt;
					// An image that cannot load (a format the phone can't decode, a 404) shows the glyph instead.
					const picture = artifactViewKind(item) === "image" && !failed ? artifactImageSource(config, sessionId, item.path, item.updatedAt) : undefined;
					// A text, markdown or HTML file shows a peek at its contents, as its gallery card does.
					const previewing = !picture && hasContentThumbnail(item) && !failed;
					return (
						<View style={{ marginLeft: position > 0 ? STRIP_GAP : 0 }}>
							<Pressable
								accessibilityRole="button"
								accessibilityLabel={`${item.name}, ${position + 1} of ${artifacts.length}`}
								accessibilityState={{ selected }}
								onPress={() => { haptics.tap(); onSelect(position); }}
								style={[styles.tile, { backgroundColor: visual.tint }, selected ? styles.tileSelected : { opacity: 0.6 }]}
							>
								{picture ? (
									<Image accessibilityIgnoresInvertColors source={imageSourceForPlatform(picture)} resizeMode="cover" onError={() => markPreviewFailed(item.path, item.updatedAt)} style={StyleSheet.absoluteFill} />
								) : previewing ? (
									<ArtifactThumbnail config={config} sessionId={sessionId} artifact={item} visible={visiblePaths.has(item.path)} versionOf={versionOf} onFailed={() => markPreviewFailed(item.path, item.updatedAt)} />
								) : (
									<Feather name={visual.icon} size={18} color={visual.color} />
								)}
							</Pressable>
						</View>
					);
				}}
			/>
		</View>
	);
}

/** The header of a gallery page: the file's name over where it sits in the list. */
function PagerTitle({ title, position, actions }: { title: string; position: string; actions: number }) {
	const styles = useThemedStyles(makeStyles);
	const { width } = useWindowDimensions();
	return (
		<View accessible accessibilityRole="header" accessibilityLabel={`${title}, ${position}`} style={[styles.pagerTitle, { maxWidth: headerTitleWidth(width, actions) }]}>
			{/* A long file name keeps its start and its extension. */}
			<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} ellipsizeMode="middle" style={styles.pagerTitleText}>{title}</Text>
			<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.pagerPosition}>{position}</Text>
		</View>
	);
}

function ArtifactBody({ config, sessionId, artifact, versionOf, onAsk }: { config: ServerConfig; sessionId: string; artifact: SessionArtifact; versionOf: (path: string) => string | undefined; onAsk: (quote?: string) => void }) {
	const kind = artifactViewKind(artifact);
	const url = artifactFileUrl(config, sessionId, artifact.path);
	if (!url) return <Unavailable title="This artifact can't be opened" message="Its path isn't one the phone can request." />;
	// A text file too large to load into the app still shows, as the page the
	// daemon serves.
	const tooLargeForText = artifact.size > MAX_ARTIFACT_TEXT_BYTES;
	if (kind === "html") return <ArtifactWeb config={config} url={url} version={artifact.updatedAt} page={{ sessionId, path: artifact.path }} />;
	if (kind === "web-media" && !showsInWebView(artifact.path, Platform.OS)) {
		return <Unavailable title="PDFs can't be shown on Android yet" message="Open it on the machine, or ask the agent about it from the ⋯ menu." />;
	}
	if (kind === "web-media" || ((kind === "markdown" || kind === "text") && tooLargeForText)) {
		return <ArtifactWeb config={config} url={url} version={artifact.updatedAt} />;
	}
	const image = kind === "image" ? artifactImageSource(config, sessionId, artifact.path, artifact.updatedAt) : undefined;
	if (image) return <ArtifactImage source={image} name={artifact.name} />;
	return <ArtifactText config={config} sessionId={sessionId} artifact={artifact} markdown={kind === "markdown"} versionOf={versionOf} onAsk={onAsk} />;
}

function ArtifactWeb({ config, url, version, page }: {
	config: ServerConfig;
	url: string;
	version: string;
	/** An HTML page, loaded with the files it links inlined. */
	page?: { sessionId: string; path: string };
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const web = useRef<WebView>(null);
	const [error, setError] = useState<string>();
	const inlined = useInlinedArtifactPage({ enabled: Boolean(page), config, sessionId: page?.sessionId ?? "", path: page?.path ?? "", version });
	// An inlined page carries AO's theme bootstrap; it takes the app's live theme, as a chat frame does.
	const theme = useRenderTheme();
	const themeRef = useRef(theme);
	themeRef.current = theme;
	// Written in for first paint (a page from memory has no theme fragment); a change is posted.
	const html = useMemo(() => inlined.html && withRenderTheme(inlined.html, themeRef.current), [inlined.html]);
	const loaded = useRef(false);
	useEffect(() => {
		if (inlined.html && loaded.current) web.current?.injectJavaScript(renderThemeScript(theme));
	}, [inlined.html, theme]);
	// Inlining failed: the page still opens from its URL.
	if (page && !inlined.html && !inlined.failed) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	return (
		<View style={styles.screen}>
			<WebView
				// A new version of the file reloads the page.
				key={`${version}|${inlined.html ? "inline" : "url"}`}
				ref={web}
				// Inlined, the page loads from memory with no origin: it needs no
				// cookie, and it cannot reach the daemon.
				source={html ? { html } : { uri: url, headers: authHeaders(config) }}
				style={styles.web}
				// Under the clear glass bar, the page scrolls on beneath it.
				contentInsetAdjustmentBehavior="automatic"
				// Android: the page keeps its own drags (panning a wide or zoomed page, a
				// pinch) instead of the pager taking them as a page turn. An HTML page is
				// then left through the strip. iOS hands the gesture over natively.
				nestedScrollEnabled
				startInLoadingState
				renderLoading={() => <View style={styles.webLoading}><ActivityIndicator color={t.accent} /></View>}
				onLoadStart={() => setError(undefined)}
				onLoadEnd={() => {
					loaded.current = true;
					if (inlined.html) web.current?.injectJavaScript(renderThemeScript(theme));
				}}
				onHttpError={(event) => setError(event.nativeEvent.statusCode === 404 ? "The worker no longer has this file." : "This artifact didn't load. Retry, or check it on that machine.")}
				onError={(event) => setError(event.nativeEvent.description || "This artifact didn't load.")}
			/>
			{error ? (
				<View accessibilityRole="alert" style={styles.webError}>
					<Feather name="alert-triangle" size={iconSize.sm} color={t.red} />
					<Text style={styles.webErrorText}>{error}</Text>
					<Pressable onPress={() => { setError(undefined); web.current?.reload(); }}><Text style={styles.retryText}>Retry</Text></Pressable>
				</View>
			) : null}
		</View>
	);
}

function ArtifactImage({ source, name }: { source: { uri: string; headers: Record<string, string> }; name: string }) {
	const styles = useThemedStyles(makeStyles);
	const [failed, setFailed] = useState(false);
	const [ratio, setRatio] = useState(1);
	useEffect(() => {
		let live = true;
		Image.getSizeWithHeaders(source.uri, source.headers).then(({ width, height }) => { if (live && width > 0 && height > 0) setRatio(width / height); }).catch(() => {});
		return () => { live = false; };
		// The source is rebuilt on each render; its URI (with the file's version) is what changes.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [source.uri]);
	if (failed) return <Unavailable title="This image didn't load" message="The phone may not support its format. Open it on the machine instead." />;
	return (
		<ScrollView style={styles.screen} contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.imageContent} maximumZoomScale={4} minimumZoomScale={1} centerContent>
			<Image
				source={imageSourceForPlatform(source)}
				accessibilityLabel={name}
				accessibilityIgnoresInvertColors
				resizeMode="contain"
				onError={() => setFailed(true)}
				style={[styles.image, { aspectRatio: ratio }]}
			/>
		</ScrollView>
	);
}

type TextState = { key: string; text?: string; error?: string };

function ArtifactText({ config, sessionId, artifact, markdown, versionOf, onAsk }: { config: ServerConfig; sessionId: string; artifact: SessionArtifact; markdown: boolean; versionOf: (path: string) => string | undefined; onAsk: (quote: string) => void }) {
	const glassInset = useGlassBarInset();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Re-read whenever the poll reports a newer version of the file.
	const key = `${sessionId}|${artifact.path}|${artifact.updatedAt}`;
	const [state, setState] = useState<TextState | null>(null);
	const [attempt, setAttempt] = useState(0);
	// A document's own images live beside it in the artifact folder and need the
	// connection's credential; a web image loads as it is, with no credential.
	const resolveImage = useCallback((url: string): MarkdownImageSource | undefined => artifactMarkdownImageSource(config, sessionId, artifact.path, url, versionOf), [artifact.path, config, sessionId, versionOf]);
	useEffect(() => {
		const controller = new AbortController();
		getArtifactText(config, sessionId, artifact.path, { signal: controller.signal })
			.then((text) => setState({ key, text }))
			.catch((cause) => { if (!controller.signal.aborted) setState({ key, error: userFacingError(cause) }); });
		return () => controller.abort();
	}, [artifact.path, attempt, config, key, sessionId]);
	// The previous version stays up while the next one loads.
	const text = state?.text;
	if (text === undefined) {
		if (state?.key === key && state.error) return <Unavailable title="Couldn't load the artifact" message={state.error} action={<Button title="Try again" icon="refresh-cw" onPress={() => setAttempt((n) => n + 1)} />} />;
		return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	}
	if (looksBinary(text)) return <Unavailable title="This file can't be shown on the phone" message={`It isn't text (${formatArtifactSize(artifact.size)}). Open it on the machine instead.`} />;
	// A long file shows its start: a text view is laid out whole, on Android on the UI thread.
	const shown = shownText(text);
	const cut = shown.truncated ? <View style={[styles.truncated, { marginTop: glassInset }]}><Text style={styles.truncatedText}>Showing the start of this file. Copy contents in the ⋯ menu copies all of it.</Text></View> : null;
	if (markdown) {
		return (
			<View style={styles.screen}>
				{cut}
				<ScrollView style={styles.screen} contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.document}>
					{/* One selectable surface per run, as chat responses are, so a selection can span paragraphs. */}
					<ChatMarkdown text={shown.text} continuousSelection onAddSelection={onAsk} resolveImage={resolveImage} imagesInPlace nestedScroll />
				</ScrollView>
			</View>
		);
	}
	return (
		<View style={styles.screen}>
			{cut}
			<ScrollView style={styles.screen} contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.code}>
				<HighlightedCodeText
					code={shown.text}
					language={artifactLanguage(artifact.path)}
					// Plain text past the cap: highlighting is skipped the same way it is for streaming code.
					streaming={artifact.size > MAX_HIGHLIGHTED_BYTES}
					style={styles.codeText}
					onAddSelection={onAsk}
				/>
			</ScrollView>
		</View>
	);
}

function Unavailable({ title, message, action }: { title: string; message?: string; action?: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.center}><EmptyState icon="file" title={title} message={message} action={action} /></View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	strip: { backgroundColor: t.bgBase, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	stripList: { flexGrow: 0, height: STRIP_HEIGHT },
	stripContent: { paddingHorizontal: space.md, paddingVertical: STRIP_PAD },
	tile: { width: STRIP_TILE, height: STRIP_TILE, borderRadius: radius.sm, borderCurve: "continuous", borderWidth: 2, borderColor: "transparent", overflow: "hidden", alignItems: "center", justifyContent: "center" },
	tileSelected: { borderColor: t.accent },
	pagerTitle: { alignItems: "center", justifyContent: "center" },
	pagerTitleText: { fontFamily: font.semibold, maxWidth: "100%", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	pagerPosition: { fontFamily: font.regular, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	center: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	web: { flex: 1, backgroundColor: t.bgBase },
	webLoading: { ...StyleSheet.absoluteFill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	webError: { position: "absolute", left: 12, right: 12, bottom: 16, minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: 12, borderCurve: "continuous", borderWidth: 1, borderColor: t.tintRed, backgroundColor: t.bgElevated, paddingHorizontal: space.md, paddingVertical: space.sm },
	webErrorText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	retryText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.caption1.fontSize, fontWeight: "600" },
	imageContent: { flexGrow: 1, alignItems: "center", justifyContent: "center", padding: space.lg },
	image: { width: "100%" },
	document: { paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xxxl },
	code: { padding: space.lg, paddingBottom: space.xxxl },
	codeText: { fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	truncated: { paddingHorizontal: space.lg, paddingVertical: space.sm, backgroundColor: t.tintAmber },
	truncatedText: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	notice: { position: "absolute", top: space.md, alignSelf: "center", backgroundColor: t.bgElevated, borderRadius: radius.pill, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, paddingHorizontal: space.lg, paddingVertical: space.sm },
	noticeText: { fontFamily: font.medium, fontSize: type.footnote.fontSize, color: t.textPrimary },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
