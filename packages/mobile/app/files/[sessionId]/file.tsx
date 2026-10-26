import * as Clipboard from "expo-clipboard";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Image, Platform, Pressable, ScrollView, Share, StyleSheet, Text, View, useWindowDimensions } from "react-native";
import { FlatList, GestureDetector } from "react-native-gesture-handler";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { SvgXml } from "react-native-svg";
import { parseUnifiedDiff } from "../../../../shared/files/diff-parser";
import { ChatMarkdown } from "../../../lib/chat/ChatMarkdown";
import { userFacingError } from "../../../lib/connectionError";
import { rowDividerWidth } from "../../../lib/divider";
import { haptics } from "../../../lib/haptics";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { ImageViewer } from "../../../lib/ImageViewer";
import { usePressFeedback } from "../../../lib/pressFeedback";
import { Spinner } from "../../../lib/Spinner";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useThemedStyles } from "../../../lib/ThemeProvider";
import { ToastHost } from "../../../lib/Toast";
import { font, fontScaleCap, radius, space, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { useToast } from "../../../lib/useToast";
import { getWorkspaceFile, sendFileFeedback, workspaceImageSource, type WorkspaceFileDetail } from "../../../lib/files/api";
import { CODE_LINE_HEIGHT, codeContentWidth, DiffLine, FoldRow, gutterWidth, InlineComment, LineCounts, ReviewTray, SelectionComposer, useCodeScale, type ComposerAction } from "../../../lib/files/components";
import { fileIcon } from "../../../lib/files/fileIcons";
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
import { SegmentedChoice, type SegmentedChoiceOption } from "../../../lib/files/SegmentedChoice";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Mode = "diff" | "file";
/** The one control over how the file reads: its diff, its text, or a markdown file rendered. */
type ViewChoice = "diff" | "file" | "preview" | "source";
type Selection = { anchor: number; focus: number };

/**
 * Where previous/next file lives. iOS steps through changed files from the
 * header's chevrons, as Mail steps through messages: UIKit's bottom toolbar is
 * only driven from iOS 18, and once shown it stays on the navigation
 * controller for every page under it. Android floats Material's toolbar over
 * the bottom of this page alone.
 */
const STEP_IN_HEADER = Platform.OS === "ios";
/** Material's floating toolbar: 64dp, and the gap it keeps above what it floats over. */
const FLOATING_TOOLBAR_CLEARANCE = 64 + space.sm;

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
	const styles = useThemedStyles(makeStyles);
	const { width } = useWindowDimensions();
	const codeScale = useCodeScale();

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
	const [composerHeight, setComposerHeight] = useState(0);
	const toast = useToast();
	// `show` is stable where the controller is not, so callbacks keep their identity.
	const showToast = toast.show;
	const insets = useSafeAreaInsets();
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
		navigation.setOptions({ title: name || "File" });
	}, [name, navigation]);

	const go = useCallback((offset: number) => {
		const target = changes[position + offset];
		if (!target) return;
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

	// Changes, then the file itself: rendered and as source for markdown, as text
	// otherwise. A file with nothing to compare shows only the ways it reads.
	const comparable = changed && !detail?.deleted;
	const readable = markdown && !detail?.binary && !detail?.deleted;
	const viewOptions: SegmentedChoiceOption<ViewChoice>[] = [
		...(comparable ? [{ key: "diff" as const, label: "Changes" }] : []),
		...(readable ? [{ key: "preview" as const, label: "Preview" }, { key: "source" as const, label: "Source" }] : comparable ? [{ key: "file" as const, label: "File" }] : []),
	];
	const view: ViewChoice = effectiveMode === "diff" ? "diff" : readable ? (rendered ? "preview" : "source") : "file";
	const chooseView = useCallback((next: ViewChoice) => {
		const nextMode: Mode = next === "diff" ? "diff" : "file";
		if (next === "preview" || next === "source") setRendered(next === "preview");
		if (nextMode !== mode) {
			setMode(nextMode);
			setSelection(null);
		}
	}, [mode]);

	const copyPath = useCallback(() => {
		void Clipboard.setStringAsync(path);
		haptics.success();
		showToast("Path copied.");
	}, [path, showToast]);
	const sharePath = useCallback(() => {
		// Dismissing the share sheet resolves; only a sheet that cannot open rejects.
		void Share.share({ message: path }).catch(() => showToast("Couldn't open the share sheet."));
	}, [path, showToast]);

	const canStep = position >= 0 && changes.length > 1;
	const atFirst = position <= 0;
	const atLast = position >= changes.length - 1;
	const stepLabel = `${position + 1} of ${changes.length}`;
	const wrappable = !image && !(markdown && rendered && effectiveMode === "file");

	// The page's header actions: on iOS the chevrons that step through the
	// changed files, then one menu for how the file reads and what to do with
	// its path. A menu's check marks a choice on both platforms; Android's menu
	// items carry no selected state for TalkBack, so the menu's own label says
	// whether lines wrap. Memoized: each new element re-registers the header's
	// items, and a line drag re-renders the page per frame.
	const headerItems = useMemo(() => {
		const pathActions = [
			<Stack.Toolbar.MenuAction key="copy" icon={fileIcon("copy")} onPress={copyPath}>Copy path</Stack.Toolbar.MenuAction>,
			<Stack.Toolbar.MenuAction key="share" icon={fileIcon("share")} onPress={sharePath}>Share</Stack.Toolbar.MenuAction>,
		];
		return (
			<Stack.Toolbar placement="right">
				{STEP_IN_HEADER && canStep ? (
					<Stack.Toolbar.Button icon={fileIcon("previous")} accessibilityLabel={`Previous changed file. This is ${stepLabel}`} disabled={atFirst} onPress={() => go(-1)} />
				) : null}
				{STEP_IN_HEADER && canStep ? (
					<Stack.Toolbar.Button icon={fileIcon("next")} accessibilityLabel={`Next changed file. This is ${stepLabel}`} disabled={atLast} onPress={() => go(1)} />
				) : null}
				<Stack.Toolbar.Menu
					icon={fileIcon("more")}
					accessibilityLabel={Platform.OS === "android" && wrappable ? `File actions, long lines ${wrap ? "wrap" : "scroll sideways"}` : "File actions"}
				>
					{wrappable ? (
						<Stack.Toolbar.MenuAction icon={fileIcon("wrapLines")} isOn={wrap} onPress={() => setWrap((value) => !value)}>Wrap lines</Stack.Toolbar.MenuAction>
					) : null}
					{readable ? (
						<Stack.Toolbar.MenuAction icon={fileIcon("preview")} isOn={view === "preview"} onPress={() => chooseView("preview")}>Preview</Stack.Toolbar.MenuAction>
					) : null}
					{readable ? (
						<Stack.Toolbar.MenuAction icon={fileIcon("source")} isOn={view === "source"} onPress={() => chooseView("source")}>Source</Stack.Toolbar.MenuAction>
					) : null}
					{/* The path's actions in a section of their own once the view's
					    choices sit above them. Android opens an inline section with a
					    divider, which at the top of the menu would rule off nothing. */}
					{wrappable || readable ? <Stack.Toolbar.Menu inline title="">{pathActions}</Stack.Toolbar.Menu> : pathActions}
				</Stack.Toolbar.Menu>
			</Stack.Toolbar>
		);
	}, [atFirst, atLast, canStep, chooseView, copyPath, go, readable, sharePath, stepLabel, view, wrap, wrappable]);

	const chosen = useMemo(() => selection ? selectedLines(rows, selection.anchor, selection.focus) : [], [rows, selection]);
	const selectionStart = selection ? Math.min(selection.anchor, selection.focus) : -1;
	const selectionEnd = selection ? Math.max(selection.anchor, selection.focus) : -1;

	// Hold-and-drag, or drag the line numbers, to select a range. Starting on an
	// end of the current selection picks that end up instead of starting over.
	const drag = useLineDrag({
		listRef: list,
		gutterWidth: gutterWidth(digits, codeScale) + 16,
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
			showToast("Comment added to the review.");
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
			showToast("Sent. The agent's reply appears in the session.");
		} catch (cause) {
			haptics.error();
			setActionError(userFacingError(cause, "Couldn't send that to the agent."));
		} finally {
			setBusy(false);
		}
	}, [chosen, config, detail, id, key, label, name, path, showToast]);

	const copySelection = useCallback(() => {
		void Clipboard.setStringAsync(chosen.map((row) => row.text).join("\n"));
		showToast(`Copied ${label.toLowerCase()}.`);
	}, [chosen, label, showToast]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="This file belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (!config && appLoading) return <View style={styles.center}>{headerItems}<Spinner size="large" accessibilityLabel="Connecting" /></View>;
	if (!config && connection === "closed") return <View style={styles.center}>{headerItems}<EmptyState icon="wifi-off" title="Machine offline" message="The file loads once the app reconnects." /></View>;

	// Where the file lives and how much changed, under the title that names it;
	// then the one control over how it reads. iOS adds the file's place among the
	// changes, which Android's toolbar shows instead.
	const place = STEP_IN_HEADER && canStep ? stepLabel : "";
	const counts = detail && changed ? <LineCounts additions={detail.additions} deletions={detail.deletions} binary={detail.binary} /> : null;
	const metaRow = place || dir || counts ? (
		<View style={styles.metaRow}>
			<Text
				accessibilityLabel={[place ? `Changed file ${place}` : "", dir ? `in ${dir}` : ""].filter(Boolean).join(", ") || undefined}
				maxFontSizeMultiplier={fontScaleCap.chrome}
				numberOfLines={1}
				ellipsizeMode="head"
				style={styles.metaText}
			>
				{[place, dir ? `${dir}/` : ""].filter(Boolean).join(" · ")}
			</Text>
			{counts}
		</View>
	) : null;
	const truncation = detail?.diffTruncated && effectiveMode === "diff"
		? "This diff is too large to show in full; the end is cut off."
		: detail?.contentTruncated && effectiveMode === "file" ? "This file is too large to show in full." : "";
	const meta = metaRow || detail?.previousPath || viewOptions.length > 1 || truncation ? (
		<View style={styles.meta}>
			{metaRow}
			{detail?.previousPath ? <Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.renamed}>{`Renamed from ${detail.previousPath}`}</Text> : null}
			{viewOptions.length > 1 ? <SegmentedChoice<ViewChoice> value={view} options={viewOptions} onChange={chooseView} accessibilityLabel="View" /> : null}
			{truncation ? <Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.warn}>{truncation}</Text> : null}
		</View>
	) : null;

	// Android's previous/next: Material's floating toolbar over the bottom of the
	// page, out of the way while a selection's composer has the bottom edge.
	const stepper = !STEP_IN_HEADER && canStep && !selection ? (
		<Stack.Toolbar placement="bottom">
			<Stack.Toolbar.Button icon={fileIcon("previous")} accessibilityLabel="Previous changed file" disabled={atFirst} onPress={() => go(-1)} />
			<Stack.Toolbar.View>
				<Text accessibilityLabel={`File ${stepLabel}`} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.stepCount}>{stepLabel}</Text>
			</Stack.Toolbar.View>
			<Stack.Toolbar.Button icon={fileIcon("next")} accessibilityLabel="Next changed file" disabled={atLast} onPress={() => go(1)} />
		</Stack.Toolbar>
	) : null;
	const composing = Boolean(selection && chosen.length && !drag.dragging);
	const stepperClearance = stepper ? FLOATING_TOOLBAR_CLEARANCE : 0;

	let body: React.ReactNode;
	if (loading && !detail) {
		body = <View style={styles.center}><EmptyState busy title={`Opening ${name}…`} /></View>;
	} else if (loadError && !detail) {
		body = <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't open this file" message={loadError} action={<Button title="Try again" icon="refresh-cw" onPress={() => void load(false)} />} /></View>;
	} else if (detail && image) {
		body = <ImageCompare detail={detail} name={name} sources={{
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
				onScrollToIndexFailed={({ index }) => list.current?.scrollToOffset({ offset: index * CODE_LINE_HEIGHT * codeScale, animated: true })}
				contentContainerStyle={{ paddingBottom: (selection ? 260 : review.comments.length ? 140 : 96) + stepperClearance }}
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
				<View style={{ width: Math.max(width, codeContentWidth(rows, digits, codeScale)) }}>{listView}</View>
			</ScrollView>
		);
	}

	return (
		<View style={styles.screen}>
			{headerItems}
			{meta}
			<View style={styles.body}>{body}</View>
			{composing ? (
				<SelectionComposer
					label={`${name} · ${label}`}
					busy={busy}
					error={actionError}
					onAction={(action, text) => void act(action, text)}
					onCopy={copySelection}
					onClose={() => setSelection(null)}
					onLayout={(event) => setComposerHeight(event.nativeEvent.layout.height)}
				/>
			) : (
				<>
					{stepper}
					<ReviewTray comments={review.comments} sending={review.sending} error={review.error} bottomOffset={stepperClearance} onSend={() => void review.send()} onRemove={review.remove} />
				</>
			)}
			<ToastHost toast={toast} bottomOffset={composing ? composerHeight + space.sm : stepper ? insets.bottom + stepperClearance + space.md : undefined} />
		</View>
	);
}

type ImageSide = { uri: string; headers: Record<string, string> };

/**
 * A changed image, before and after. Each pane opens the full-screen viewer,
 * where it zooms. The daemon's images need the connection's Bearer header,
 * which React Native's Android image loader forwards only for an array source.
 */
function ImageCompare({ detail, name, sources }: { detail: WorkspaceFileDetail; name: string; sources: { before?: ImageSide; after?: ImageSide } }) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button", { foreground: true });
	const [viewing, setViewing] = useState<{ label: string; source: ImageSide }>();
	const panes = [
		sources.before ? { label: detail.deleted ? "Deleted" : "Before", source: sources.before } : null,
		sources.after ? { label: detail.status === "added" ? "Added" : sources.before ? "After" : statusLabel[detail.status], source: sources.after } : null,
	].filter((pane): pane is NonNullable<typeof pane> => Boolean(pane));
	return (
		<ScrollView contentContainerStyle={styles.images}>
			{panes.map((pane) => (
				<View key={pane.label} style={styles.imagePane}>
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.imageLabel}>{pane.label}</Text>
					<Pressable
						accessibilityRole="imagebutton"
						accessibilityLabel={`${pane.label} image`}
						accessibilityHint="Opens it full screen to zoom"
						onPress={() => setViewing(pane)}
						android_ripple={feedback.android_ripple}
						style={({ pressed }) => [styles.imageFrame, pressed && feedback.pressed]}
					>
						<Image accessibilityIgnoresInvertColors source={Platform.OS === "android" ? [pane.source] : pane.source} resizeMode="contain" style={styles.image} />
					</Pressable>
				</View>
			))}
			{viewing ? <ImageViewer visible name={`${name}, ${viewing.label.toLowerCase()}`} source={viewing.source} onClose={() => setViewing(undefined)} /> : null}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	body: { flex: 1 },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
	meta: { paddingHorizontal: space.lg, paddingTop: space.sm, paddingBottom: space.md, gap: space.sm, borderBottomWidth: rowDividerWidth, borderColor: t.borderSubtle, backgroundColor: t.bgBase },
	metaRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
	metaText: { flex: 1, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, color: t.textTertiary },
	stepCount: { ...type.footnote, fontFamily: font.medium, color: t.textSecondary, paddingHorizontal: space.sm },
	renamed: { ...type.footnote, color: t.blue },
	warn: { ...type.footnote, color: t.amber },
	preview: { padding: space.lg },
	markdown: { padding: space.lg, paddingBottom: 120 },
	images: { padding: space.lg, gap: space.lg, paddingBottom: 120 },
	imagePane: { gap: space.xs },
	imageLabel: { ...type.footnote, fontFamily: font.semibold, color: t.textSecondary },
	imageFrame: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderSubtle },
	image: { width: "100%", aspectRatio: 1 },
});
