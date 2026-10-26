import * as Clipboard from "expo-clipboard";
import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { AccessibilityInfo, ActivityIndicator, Image, Pressable, ScrollView, StyleSheet, Text, View, useWindowDimensions } from "react-native";
import { FlatList, GestureDetector } from "react-native-gesture-handler";
import { SvgXml } from "react-native-svg";
import { parseUnifiedDiff } from "../../../../shared/files/diff-parser";
import { ChatMarkdown } from "../../../lib/chat/ChatMarkdown";
import { userFacingError } from "../../../lib/connectionError";
import { haptics } from "../../../lib/haptics";
import { headerActionStyle, headerGlyphStyle } from "../../../lib/headerAction";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { Feather } from "../../../lib/icons";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { font, fontScaleCap, iconSize, press, radius, space, touchTarget, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { getWorkspaceFile, sendFileFeedback, workspaceImageSource, type WorkspaceFileDetail } from "../../../lib/files/api";
import { codeContentWidth, DiffLine, FoldRow, gutterWidth, InlineComment, LineCounts, ReviewTray, SegmentedTabs, SelectionComposer, FileTypeIcon, type ComposerAction } from "../../../lib/files/components";
import {
	annotationTarget,
	changeGroups,
	commentAnchor,
	CONTEXT_STEP,
	contentLines,
	diffDisplayRows,
	fileDisplayRows,
	isImagePath,
	isMarkdownPath,
	isSvgPath,
	languageForPath,
	orderedChanges,
	selectedLines,
	selectionLabel,
	splitPath,
	statusLabel,
	withInlineComments,
	type DisplayRow,
} from "../../../lib/files/model";
import { addReviewComment, reviewKey } from "../../../lib/files/reviewStore";
import { useReviewSender } from "../../../lib/files/useReviewSender";
import { useLineDrag } from "../../../lib/files/useLineDrag";
import { useWorkspaceManifest } from "../../../lib/files/useWorkspace";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Mode = "diff" | "file";
type Selection = { anchor: number; focus: number };

export default function SessionFileScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionFileContent /></HostScope> : <SessionFileContent />;
}

function SessionFileContent() {
	const params = useLocalSearchParams<{ sessionId: string; path: string; mode?: Mode; previousPath?: string; hostId?: string }>();
	const id = String(params.sessionId ?? "");
	const path = String(params.path ?? "");
	const router = useRouter();
	const navigation = useNavigation();
	const { config, currentHostId, connection, loading: appLoading } = useApp();
	const hostMatches = hostRouteMatches(params.hostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { width } = useWindowDimensions();

	const { manifest } = useWorkspaceManifest(hostMatches ? config : null, id, hostMatches);
	const changes = useMemo(() => manifest ? orderedChanges(changeGroups(manifest)) : [], [manifest]);
	const position = changes.findIndex((file) => file.path === path);
	const listed = position >= 0 ? changes[position] : undefined;

	const [detail, setDetail] = useState<WorkspaceFileDetail | null>(null);
	const [loading, setLoading] = useState(true);
	const [loadError, setLoadError] = useState<string>();
	const [mode, setMode] = useState<Mode>(params.mode === "file" ? "file" : "diff");
	const [wrap, setWrap] = useState(true);
	const [rendered, setRendered] = useState(true);
	const [revealed, setRevealed] = useState<Record<string, number>>({});
	const [selection, setSelection] = useState<Selection | null>(null);
	const [busy, setBusy] = useState(false);
	const [actionError, setActionError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const list = useRef<FlatList<DisplayRow>>(null);
	const request = useRef(0);

	const key = reviewKey(currentHostId, id);
	const review = useReviewSender(config, key, id);

	// What says the file changed under us: its fingerprint in the manifest, or the
	// whole workspace version for a file the manifest does not list.
	const fingerprint = listed?.fileFingerprint || manifest?.workspaceVersion || "";
	const load = useCallback(async (quiet: boolean) => {
		if (!config || !hostMatches || !path) return;
		const ticket = ++request.current;
		if (!quiet) setLoading(true);
		try {
			const next = await getWorkspaceFile(config, id, path);
			if (ticket !== request.current) return;
			setDetail(next);
			setLoadError(undefined);
		} catch (cause) {
			if (ticket === request.current && !quiet) setLoadError(userFacingError(cause, "Couldn't open this file."));
		} finally {
			if (ticket === request.current) setLoading(false);
		}
	}, [config, hostMatches, id, path]);

	// A new path starts from a clean slate; the same path reloads quietly when the
	// agent edits it, keeping the reader's place.
	useEffect(() => {
		setDetail(null);
		setRevealed({});
		setSelection(null);
		setActionError(undefined);
		void load(false);
	}, [load]);
	const seenFingerprint = useRef(fingerprint);
	useEffect(() => {
		if (!fingerprint || seenFingerprint.current === fingerprint) return;
		const first = !seenFingerprint.current;
		seenFingerprint.current = fingerprint;
		if (!first) void load(true);
	}, [fingerprint, load]);

	const changed = Boolean(detail ? detail.status !== "unmodified" || detail.diff : listed);
	const effectiveMode: Mode = !changed ? "file" : detail?.deleted ? "diff" : mode;
	const lines = useMemo(() => detail ? contentLines(detail.deleted ? undefined : detail.content, detail.contentTruncated || detail.binary) : null, [detail]);
	const parsed = useMemo(() => detail?.diff ? parseUnifiedDiff(detail.diff) : [], [detail?.diff]);
	const baseRows = useMemo<DisplayRow[]>(() => {
		if (!detail || detail.binary) return [];
		return effectiveMode === "diff" ? diffDisplayRows(parsed, lines, revealed) : fileDisplayRows(lines ?? []);
	}, [detail, effectiveMode, lines, parsed, revealed]);
	// Comments on this file, queued and sent, drawn under the lines they are about.
	const fileComments = useMemo(() => review.all.filter((comment) => comment.target.path === path), [path, review.all]);
	const commentById = useMemo(() => new Map(fileComments.map((comment) => [comment.id, comment])), [fileComments]);
	const rows = useMemo(() => withInlineComments(baseRows, fileComments), [baseRows, fileComments]);
	const digits = useMemo(() => String(rows.reduce((max, row) => row.type === "line" ? Math.max(max, row.row.newNo ?? 0, row.row.oldNo ?? 0) : max, 0)).length, [rows]);
	const language = languageForPath(path);
	const { name, dir } = splitPath(path);
	const markdown = isMarkdownPath(path);
	const image = isImagePath(path);
	const svg = isSvgPath(path);

	// Lines with a comment card under them, so a screen reader hears "has a comment" there.
	const commented = useMemo(() => new Set(fileComments.flatMap((comment) => comment.anchor ? [`${comment.anchor.side}:${comment.anchor.line}`] : [])), [fileComments]);

	useLayoutEffect(() => {
		navigation.setOptions({
			title: name || "File",
			headerRight: hostMatches && !image && !(markdown && rendered && effectiveMode === "file") ? () => (
				<Pressable
					accessibilityRole="switch"
					accessibilityLabel="Wrap long lines"
					accessibilityState={{ checked: wrap }}
					hitSlop={10}
					onPress={() => { haptics.select(); setWrap((value) => !value); }}
					style={headerActionStyle}
				>
					<Feather name="wrap-text" size={iconSize.md} color={wrap ? t.textPrimary : t.textTertiary} style={headerGlyphStyle} />
				</Pressable>
			) : undefined,
		});
	}, [effectiveMode, hostMatches, image, markdown, name, navigation, rendered, t.textPrimary, t.textTertiary, wrap]);

	useEffect(() => {
		if (!notice) return;
		AccessibilityInfo.announceForAccessibility(notice);
		const timer = setTimeout(() => setNotice(undefined), 3_000);
		return () => clearTimeout(timer);
	}, [notice]);

	const go = useCallback((offset: number) => {
		const target = changes[position + offset];
		if (!target) return;
		haptics.select();
		router.setParams({ path: target.path, previousPath: target.previousPath ?? "", mode: "diff" });
	}, [changes, position, router]);

	const onSelect = useCallback((index: number) => {
		setActionError(undefined);
		setSelection((current) => current && current.anchor === index && current.focus === index ? null : { anchor: index, focus: index });
	}, []);
	const onExtend = useCallback((index: number) => {
		setActionError(undefined);
		setSelection((current) => current ? { anchor: current.anchor, focus: index } : { anchor: index, focus: index });
	}, []);
	const expand = useCallback((foldId: string) => {
		setRevealed((current) => ({ ...current, [foldId]: (current[foldId] ?? 0) + CONTEXT_STEP }));
	}, []);

	const chosen = useMemo(() => selection ? selectedLines(rows, selection.anchor, selection.focus) : [], [rows, selection]);
	const selectionStart = selection ? Math.min(selection.anchor, selection.focus) : -1;
	const selectionEnd = selection ? Math.max(selection.anchor, selection.focus) : -1;

	// Hold-and-drag, or drag the line numbers, to select a range. Starting on an
	// end of the current selection picks that end up instead of starting over.
	const drag = useLineDrag({
		listRef: list,
		gutterWidth: gutterWidth(digits) + 16,
		enabled: rows.length > 0,
		onStart: (index) => {
			setActionError(undefined);
			setSelection((current) => {
				if (current) {
					const low = Math.min(current.anchor, current.focus);
					const high = Math.max(current.anchor, current.focus);
					if (index === low && low !== high) return { anchor: high, focus: index };
					if (index === high && low !== high) return { anchor: low, focus: index };
				}
				return { anchor: index, focus: index };
			});
		},
		onMove: (index) => setSelection((current) => current ? { anchor: current.anchor, focus: index } : { anchor: index, focus: index }),
		onEnd: () => {},
	});
	const resetDrag = drag.reset;
	useEffect(() => { resetDrag(); }, [path, effectiveMode, resetDrag]);

	// The composer covers the bottom of the list. When the end of a selection
	// lands among the last rows on screen, lift it into view above the composer.
	const lastVisible = useRef(0);
	const onViewable = useRef(({ viewableItems }: { viewableItems: Array<{ index: number | null }> }) => {
		lastVisible.current = viewableItems.reduce((max, item) => Math.max(max, item.index ?? 0), 0);
	}).current;
	useEffect(() => {
		// Mid-drag the finger drives the scroll; lift the selection only once it lands.
		if (!selection || drag.dragging || selection.focus < lastVisible.current - 10) return;
		const timer = setTimeout(() => list.current?.scrollToIndex({ index: selection.focus, viewPosition: 0.3, animated: true }), 60);
		return () => clearTimeout(timer);
	}, [drag.dragging, selection]);
	const label = chosen.length ? selectionLabel(chosen) : "";

	const act = useCallback(async (action: ComposerAction, text: string) => {
		if (!config || !detail || !chosen.length) return;
		const feedback = text.trim();
		const target = annotationTarget({ path, previousPath: detail.previousPath, workspaceVersion: detail.workspaceVersion, fileFingerprint: detail.fileFingerprint }, chosen);
		const anchor = commentAnchor(chosen);
		if (action === "comment") {
			addReviewComment(key, { label: `${name} · ${label}`, feedback, target, anchor });
			haptics.success();
			setSelection(null);
			setNotice(`Comment added to the review.`);
			return;
		}
		setBusy(true);
		setActionError(undefined);
		try {
			await sendFileFeedback(config, id, [{ target, feedback }]);
			// What was sent stays on the line, like a posted review comment.
			addReviewComment(key, { label: `${name} · ${label}`, feedback, target, anchor, status: "sent" });
			haptics.success();
			setSelection(null);
			setNotice("Sent. The agent's reply appears in the session.");
		} catch (cause) {
			haptics.error();
			setActionError(userFacingError(cause, "Couldn't send that to the agent."));
		} finally {
			setBusy(false);
		}
	}, [chosen, config, detail, id, key, label, name, path]);

	const copySelection = useCallback(() => {
		void Clipboard.setStringAsync(chosen.map((row) => row.text).join("\n"));
		setNotice(`Copied ${label.toLowerCase()}.`);
	}, [chosen, label]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="This file belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (!config && appLoading) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="The file loads once the app reconnects." /></View>;

	const pathBar = (
		<View style={styles.pathBar}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={`Path ${path}. Copy path`}
				onLongPress={() => { void Clipboard.setStringAsync(path); haptics.success(); setNotice("Path copied."); }}
				onPress={() => { void Clipboard.setStringAsync(path); haptics.success(); setNotice("Path copied."); }}
				style={styles.pathPress}
			>
				<FileTypeIcon path={path} status={detail && changed ? detail.status : undefined} size={18} />
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} ellipsizeMode="head" style={styles.pathText}>
					{dir ? <Text style={styles.pathDir}>{`${dir}/`}</Text> : null}
					<Text style={styles.pathName}>{name}</Text>
				</Text>
				{detail && changed ? <LineCounts additions={detail.additions} deletions={detail.deletions} binary={detail.binary} /> : null}
			</Pressable>
			{detail?.previousPath ? <Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.renamed}>{`Renamed from ${detail.previousPath}`}</Text> : null}
			{changed && !detail?.deleted ? (
				<SegmentedTabs<Mode>
					value={effectiveMode}
					onChange={(next) => { setMode(next); setSelection(null); }}
					options={[{ key: "diff", label: "Changes", icon: "file-diff" }, { key: "file", label: "File", icon: "file-text" }]}
				/>
			) : null}
			{markdown && effectiveMode === "file" && !detail?.binary ? (
				<SegmentedTabs<"rendered" | "source">
					value={rendered ? "rendered" : "source"}
					onChange={(next) => setRendered(next === "rendered")}
					options={[{ key: "rendered", label: "Preview", icon: "eye" }, { key: "source", label: "Source", icon: "code" }]}
				/>
			) : null}
			{detail?.diffTruncated && effectiveMode === "diff" ? <Text style={styles.warn}>This diff is too large to show in full; the end is cut off.</Text> : null}
			{detail?.contentTruncated && effectiveMode === "file" ? <Text style={styles.warn}>This file is too large to show in full.</Text> : null}
		</View>
	);

	const toolbar = position >= 0 && changes.length > 1 && !selection ? (
		<View accessibilityRole="toolbar" style={styles.toolbar}>
			<Pressable accessibilityRole="button" accessibilityLabel="Previous changed file" accessibilityState={{ disabled: position === 0 }} disabled={position === 0} onPress={() => go(-1)} style={({ pressed }) => [styles.toolButton, position === 0 && styles.disabled, pressed && { opacity: press.opacity }]}>
				<Feather name="chevron-left" size={iconSize.lg} color={t.textSecondary} />
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.toolText}>Previous</Text>
			</Pressable>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} accessibilityLabel={`File ${position + 1} of ${changes.length}`} style={styles.toolCount}>{`${position + 1} of ${changes.length}`}</Text>
			<Pressable accessibilityRole="button" accessibilityLabel="Next changed file" accessibilityState={{ disabled: position === changes.length - 1 }} disabled={position === changes.length - 1} onPress={() => go(1)} style={({ pressed }) => [styles.toolButton, position === changes.length - 1 && styles.disabled, pressed && { opacity: press.opacity }]}>
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.toolText}>Next</Text>
				<Feather name="chevron-right" size={iconSize.lg} color={t.textSecondary} />
			</Pressable>
		</View>
	) : null;

	let body: React.ReactNode;
	if (loading && !detail) {
		body = <View style={styles.center}><ActivityIndicator color={t.accent} /><Text style={styles.copy}>Opening {name}…</Text></View>;
	} else if (loadError && !detail) {
		body = <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't open this file" message={loadError} action={<Button title="Try again" icon="refresh-cw" variant="ghost" onPress={() => void load(false)} />} /></View>;
	} else if (detail && image) {
		body = <ImageCompare detail={detail} sources={{
			before: detail.status !== "added" && changed ? workspaceImageSource(config!, id, path, "before", detail.workspaceVersion) : undefined,
			after: !detail.deleted ? workspaceImageSource(config!, id, path, "after", detail.workspaceVersion) : undefined,
		}} />;
	} else if (detail && svg && effectiveMode === "file" && lines && !detail.binary) {
		body = <ScrollView contentContainerStyle={styles.preview}><SvgXml xml={detail.content} width="100%" height={width} /></ScrollView>;
	} else if (detail?.binary) {
		body = <View style={styles.center}><EmptyState icon="file" title="Binary file" message="This file isn't text, so there's nothing to show line by line." /></View>;
	} else if (detail && markdown && rendered && effectiveMode === "file") {
		body = <ScrollView contentContainerStyle={styles.markdown}><ChatMarkdown text={detail.content} /></ScrollView>;
	} else if (detail && !rows.length) {
		body = <View style={styles.center}><EmptyState icon={effectiveMode === "diff" ? "check" : "file"} title={effectiveMode === "diff" ? "No changes in this file" : "Empty file"} message={effectiveMode === "diff" ? "It matches the base branch." : undefined} /></View>;
	} else {
		const listView = (
			<GestureDetector gesture={drag.gesture}>
			<FlatList
				key={`${path}:${effectiveMode}`}
				ref={list}
				{...drag.listProps}
				scrollEnabled={!drag.dragging}
				data={rows}
				keyExtractor={(row) => row.key}
				extraData={`${selection?.anchor}:${selection?.focus}:${wrap}:${commented.size}:${commentById.size}`}
				initialNumToRender={60}
				maxToRenderPerBatch={60}
				windowSize={15}
				onViewableItemsChanged={onViewable}
				onScrollToIndexFailed={({ index }) => list.current?.scrollToOffset({ offset: index * 19, animated: true })}
				contentContainerStyle={{ paddingBottom: selection ? 260 : review.comments.length ? 140 : 96 }}
				renderItem={({ item, index }) => item.type === "fold" ? (
					<FoldRow hidden={item.hidden} section={item.section} expandable={item.expandable} onExpand={() => expand(item.id)} />
				) : item.type === "comment" ? (
					commentById.get(item.commentId) ? <InlineComment comment={commentById.get(item.commentId)!} digits={digits} onRemove={review.remove} /> : null
				) : (
					<DiffLine
						row={item.row}
						index={index}
						language={language}
						wrap={wrap}
						digits={digits}
						selected={index >= selectionStart && index <= selectionEnd}
						edge={index === selectionStart && index === selectionEnd ? "both" : index === selectionStart ? "start" : index === selectionEnd ? "end" : undefined}
						commented={commented.has(item.row.kind === "del" ? `old:${item.row.oldNo}` : `new:${item.row.newNo}`)}
						selectionActive={Boolean(selection)}
						onSelect={onSelect}
						onExtend={onExtend}
					/>
				)}
			/>
			</GestureDetector>
		);
		body = wrap ? listView : (
			<ScrollView horizontal bounces={false} contentContainerStyle={{ minWidth: width }}>
				<View style={{ width: Math.max(width, codeContentWidth(rows, digits)) }}>{listView}</View>
			</ScrollView>
		);
	}

	return (
		<View style={styles.screen}>
			{pathBar}
			<View style={styles.body}>{body}</View>
			{notice ? <View pointerEvents="none" style={styles.notice}><Text style={styles.noticeText}>{notice}</Text></View> : null}
			{selection && chosen.length && !drag.dragging ? (
				<SelectionComposer
					label={`${name} · ${label}`}
					busy={busy}
					error={actionError}
					onAction={(action, text) => void act(action, text)}
					onCopy={copySelection}
					onClose={() => setSelection(null)}
				/>
			) : (
				<>
					{toolbar}
					<ReviewTray comments={review.comments} sending={review.sending} error={review.error} bottomOffset={toolbar ? TOOLBAR_HEIGHT : 0} onSend={() => void review.send()} onRemove={review.remove} />
				</>
			)}
		</View>
	);
}

const TOOLBAR_HEIGHT = 56;

function ImageCompare({ detail, sources }: { detail: WorkspaceFileDetail; sources: { before?: { uri: string; headers: Record<string, string> }; after?: { uri: string; headers: Record<string, string> } } }) {
	const styles = useThemedStyles(makeStyles);
	const panes = [
		sources.before ? { label: detail.deleted ? "Deleted" : "Before", source: sources.before } : null,
		sources.after ? { label: detail.status === "added" ? "Added" : sources.before ? "After" : statusLabel[detail.status], source: sources.after } : null,
	].filter((pane): pane is NonNullable<typeof pane> => Boolean(pane));
	return (
		<ScrollView contentContainerStyle={styles.images}>
			{panes.map((pane) => (
				<View key={pane.label} style={styles.imagePane}>
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.imageLabel}>{pane.label}</Text>
					<Image accessibilityLabel={`${pane.label} image`} source={pane.source} resizeMode="contain" style={styles.image} />
				</View>
			))}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	body: { flex: 1 },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
	copy: { ...type.footnote, color: t.textTertiary },
	pathBar: { paddingHorizontal: space.lg, paddingTop: space.sm, paddingBottom: space.md, gap: space.sm, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, backgroundColor: t.bgBase },
	pathPress: { minHeight: touchTarget - space.md, flexDirection: "row", alignItems: "center", gap: space.sm },
	pathText: { flex: 1, fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
	pathDir: { color: t.textTertiary },
	pathName: { color: t.textPrimary },
	renamed: { ...type.footnote, color: t.blue },
	warn: { ...type.footnote, color: t.amber },
	toolbar: { position: "absolute", left: 0, right: 0, bottom: 0, height: TOOLBAR_HEIGHT, flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: space.sm, backgroundColor: t.bgSurface, borderTopWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	toolButton: { minHeight: touchTarget, minWidth: touchTarget * 2, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xxs, paddingHorizontal: space.sm },
	toolText: { fontFamily: font.medium, fontSize: type.subheadline.fontSize, color: t.textSecondary },
	toolCount: { fontFamily: font.medium, fontSize: type.footnote.fontSize, color: t.textTertiary },
	disabled: { opacity: 0.35 },
	notice: { position: "absolute", top: space.md, alignSelf: "center", backgroundColor: t.bgElevated, borderRadius: radius.pill, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, paddingHorizontal: space.lg, paddingVertical: space.sm },
	noticeText: { fontFamily: font.medium, fontSize: type.footnote.fontSize, color: t.textPrimary },
	preview: { padding: space.lg },
	markdown: { padding: space.lg, paddingBottom: 120 },
	images: { padding: space.lg, gap: space.lg, paddingBottom: 120 },
	imagePane: { gap: space.xs },
	imageLabel: { fontFamily: font.semibold, fontSize: type.caption2.fontSize, letterSpacing: 0.9, textTransform: "uppercase", color: t.textTertiary },
	image: { width: "100%", aspectRatio: 1, borderRadius: radius.md, backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
});
