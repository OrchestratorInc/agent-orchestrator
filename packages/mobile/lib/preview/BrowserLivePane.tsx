import { Stack } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { Image, StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { scheduleOnRN } from "react-native-worklets";
import { BrowserLiveClient, type BrowserLiveCommand, type BrowserLiveState } from "../browserLive";
import { browserJPEGDataURI, browserWheelDeltaFromDrag, containBrowserFrame, normalizedFramePoint, type BrowserFrameRect } from "../browserLiveProtocol";
import { useApp } from "../store";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { ToastHost } from "../Toast";
import { duration, radius, space } from "../tokens";
import { Button, EmptyState } from "../ui";
import { useReducedMotion } from "../useReducedMotion";
import { useToast } from "../useToast";
import { bottomToolbarHeight, browserMenu, browserToolbar, hasBottomToolbar, type BrowserControls, type BrowserTab } from "./browserControls";
import { FRAME_ZOOM_SNAP, pinchZoom, unzoomed, unzoomPoint, type FramePoint, type FrameSize, type FrameZoom } from "./frameZoom";
import { LoadingBar } from "./LoadingBar";
import { OpenURLPrompt } from "./OpenURLPrompt";

type BrowserStatus = "connecting" | "open" | "error";
type NavigateAction = "back" | "forward" | "reload" | "stop";

/** How far a finger may wander before a tap becomes a scroll. */
const DRAG_SLOP = 7;
/** A held finger still clicks when it lifts, as it always has here. */
const TAP_MAX_MS = 10_000;
/** The mark a tap leaves on the frame: about a fingertip. */
const TOUCH_MARK = 36;
const ignoreActivation = () => {};

export type BrowserLivePaneProps = {
	sessionID: string;
	onDisabled?(): void;
	/** The desktop page's title as it changes, or undefined while there is none to show. */
	onTitle?(title: string | undefined): void;
	/**
	 * The page's own trailing header item, memoised. A screen has one right
	 * toolbar and the pane draws it while it shows, because on iOS the
	 * browser's controls go there too, so the page hands its item over.
	 */
	headerItem?: ReactElement | null;
};

export function BrowserLivePane({ sessionID, onDisabled, onTitle, headerItem = null }: BrowserLivePaneProps) {
	const { config } = useApp();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const reduceMotion = useReducedMotion();
	const toast = useToast();
	const showNotice = toast.show;
	const client = useRef<BrowserLiveClient | null>(null);
	const [attempt, setAttempt] = useState(0);
	const [status, setStatus] = useState<BrowserStatus>("connecting");
	const [error, setError] = useState<string>();
	const [page, setPage] = useState<BrowserLiveState | null>(null);
	const [frameURI, setFrameURI] = useState<string>();
	const [frameSize, setFrameSize] = useState({ width: 1, height: 1 });
	const [viewportSize, setViewportSize] = useState({ width: 1, height: 1 });
	const frameRect = useMemo(() => containBrowserFrame(viewportSize, frameSize), [frameSize, viewportSize]);
	const frameRectRef = useRef<BrowserFrameRect>(frameRect);
	frameRectRef.current = frameRect;
	// Width of the desktop page in its own pixels. A frame can be captured at a
	// different resolution than the page is laid out at, so prefer the reported
	// viewport and fall back to the frame until the first state arrives.
	const pageWidth = useRef(0);
	const frameWidth = useRef(1);
	frameWidth.current = frameSize.width;
	const blank = page !== null && !page.url;

	useEffect(() => {
		setPage(null);
		if (!config) {
			setStatus("error");
			setError("Pair this phone with the desktop app first.");
			return;
		}
		if (!sessionID) {
			setStatus("error");
			setError("This preview doesn't identify a session.");
			return;
		}
		let disposed = false;
		setStatus("connecting");
		setError(undefined);
		const live = new BrowserLiveClient(config, sessionID, {
			onStatus: (next, message) => {
				if (disposed || next === "closed") return;
				setStatus(next === "open" ? "open" : next === "connecting" ? "connecting" : "error");
				if (next === "open") {
					setError(undefined);
					// A refused command leaves the stream open: say why, and keep the page up.
					if (message) showNotice(message);
				} else {
					setError(message);
				}
			},
			onDisabled: () => { if (!disposed) onDisabled?.(); },
			onState: (state) => {
				if (disposed) return;
				pageWidth.current = state.width;
				setPage(state);
			},
			onFrame: (frame) => {
				if (disposed) return;
				try {
					setFrameSize({ width: frame.width, height: frame.height });
					setFrameURI(browserJPEGDataURI(frame.jpeg));
				} catch {
					setStatus("error");
					setError("This mobile build couldn't display the browser frame.");
				}
			},
		});
		client.current = live;
		live.connect();

		return () => {
			disposed = true;
			live?.close();
			if (client.current === live) client.current = null;
		};
	}, [attempt, config, onDisabled, sessionID, showNotice]);

	// The bar is titled with the desktop's page while one is showing.
	const pageTitle = status === "open" && page?.url ? page.title.trim() || undefined : undefined;
	useEffect(() => {
		onTitle?.(pageTitle);
	}, [onTitle, pageTitle]);
	useEffect(() => () => onTitle?.(undefined), [onTitle]);

	const send = useCallback((command: BrowserLiveCommand) => client.current?.send(command), []);
	const navigate = useCallback((action: NavigateAction) => send({ type: "navigate", payload: { action } }), [send]);
	const [prompting, setPrompting] = useState(false);
	const openURL = useCallback((url: string) => send({ type: "navigate", payload: { action: "open", url } }), [send]);
	const closePrompt = useCallback(() => setPrompting(false), []);

	// Each state message carries a new tab list, so it is compared by content
	// to keep the toolbars from rebuilding while nothing in them changed.
	const tabsJSON = JSON.stringify(page?.tabs ?? []);
	const tabs = useMemo(() => JSON.parse(tabsJSON) as BrowserTab[], [tabsJSON]);
	const canGoBack = Boolean(page?.canGoBack);
	const canGoForward = Boolean(page?.canGoForward);
	const loading = Boolean(page?.loading);
	const controls = useMemo<BrowserControls>(() => ({
		enabled: status === "open",
		canGoBack,
		canGoForward,
		loading,
		tabs,
		tint: t.accent,
		disabledTint: t.textFaint,
		surface: t.bgElevated,
		onBack: () => navigate("back"),
		onForward: () => navigate("forward"),
		onReload: () => navigate("reload"),
		onStop: () => navigate("stop"),
		onOpenURL: () => setPrompting(true),
		onNewTab: () => send({ type: "tab", payload: { action: "new" } }),
		onSelectTab: (tabId) => send({ type: "tab", payload: { action: "select", tabId } }),
		onCloseTab: (tabId) => send({ type: "tab", payload: { action: "close", tabId } }),
	}), [canGoBack, canGoForward, loading, navigate, send, status, t.accent, t.bgElevated, t.textFaint, tabs]);
	// Android: a bottom toolbar. iOS: one menu in the header.
	const toolbarShown = hasBottomToolbar();
	const bottomBar = useMemo(() => toolbarShown ? browserToolbar(controls) : null, [controls, toolbarShown]);
	// Memoized apart from the controls, so with a bottom toolbar the header's
	// items register again only when the page's own item changes.
	const headerMenu = useMemo(() => toolbarShown ? null : browserMenu(controls), [controls, toolbarShown]);
	const headerItems = useMemo(() => headerMenu || headerItem ? (
		<Stack.Toolbar placement="right">
			{headerMenu}
			{headerItem}
		</Stack.Toolbar>
	) : null, [headerItem, headerMenu]);

	// Gestures arrive in viewport points with any zoom already undone.
	const tapAt = useCallback((x: number, y: number) => {
		const point = normalizedFramePoint(frameRectRef.current, { x, y });
		if (!point) return;
		send({ type: "input", payload: { kind: "pointer", phase: "down", ...point, button: "left" } });
		send({ type: "input", payload: { kind: "pointer", phase: "up", ...point, button: "left" } });
	}, [send]);
	const scrollBy = useCallback((deltaX: number, deltaY: number, zoom: number) => {
		// A magnified frame shows fewer desktop pixels per point, so the drag is
		// scaled by the zoom too and the page keeps pace with the finger.
		const fitted = frameRectRef.current.width / (pageWidth.current > 1 ? pageWidth.current : frameWidth.current);
		send({ type: "input", payload: { kind: "wheel", ...browserWheelDeltaFromDrag(deltaX, deltaY, fitted * zoom) } });
	}, [send]);

	const viewport = useSharedValue<FrameSize>({ width: 1, height: 1 });
	const zoomScale = useSharedValue(1);
	const zoomX = useSharedValue(0);
	const zoomY = useSharedValue(0);
	const pinchFrom = useSharedValue<FrameZoom>(unzoomed);
	const pinchFocal = useSharedValue<FramePoint>({ x: 0, y: 0 });
	const markX = useSharedValue(0);
	const markY = useSharedValue(0);
	const markScale = useSharedValue(1);
	const markOpacity = useSharedValue(0);
	const interactive = status === "open" && Boolean(frameURI) && !blank;
	// Another session's page starts at its fitted size.
	useEffect(() => {
		zoomScale.value = 1;
		zoomX.value = 0;
		zoomY.value = 0;
	}, [sessionID, zoomScale, zoomX, zoomY]);

	// One finger taps (a click on the page) or drags (scrolls it); two pinch to
	// magnify the frame and pan across it. The pinch runs on the UI thread, and
	// only the commands for the desktop cross to JS.
	const gesture = useMemo(() => {
		const tap = Gesture.Tap()
			.enabled(interactive)
			.maxDistance(DRAG_SLOP)
			.maxDuration(TAP_MAX_MS)
			.onBegin((event) => {
				markX.value = event.x;
				markY.value = event.y;
				markScale.value = reduceMotion ? 1 : 0.6;
				if (!reduceMotion) markScale.value = withTiming(1, { duration: duration.base });
				markOpacity.value = withTiming(1, { duration: duration.fast });
			})
			.onEnd((event, success) => {
				if (!success) return;
				const point = unzoomPoint({ x: event.x, y: event.y }, { scale: zoomScale.value, x: zoomX.value, y: zoomY.value }, viewport.value);
				scheduleOnRN(tapAt, point.x, point.y);
			})
			.onFinalize(() => {
				markOpacity.value = withTiming(0, { duration: duration.slow });
			});
		const pan = Gesture.Pan()
			.enabled(interactive)
			.maxPointers(1)
			.minDistance(DRAG_SLOP)
			.onChange((event) => {
				scheduleOnRN(scrollBy, event.changeX, event.changeY, zoomScale.value);
			});
		const pinch = Gesture.Pinch()
			.enabled(interactive)
			.onStart((event) => {
				pinchFrom.value = { scale: zoomScale.value, x: zoomX.value, y: zoomY.value };
				pinchFocal.value = { x: event.focalX, y: event.focalY };
			})
			.onUpdate((event) => {
				const next = pinchZoom(pinchFrom.value, pinchFocal.value, { x: event.focalX, y: event.focalY }, event.scale, viewport.value);
				zoomScale.value = next.scale;
				zoomX.value = next.x;
				zoomY.value = next.y;
			})
			.onEnd(() => {
				if (zoomScale.value >= FRAME_ZOOM_SNAP) return;
				zoomScale.value = reduceMotion ? 1 : withTiming(1, { duration: duration.base });
				zoomX.value = reduceMotion ? 0 : withTiming(0, { duration: duration.base });
				zoomY.value = reduceMotion ? 0 : withTiming(0, { duration: duration.base });
			});
		return Gesture.Race(pinch, pan, tap);
	}, [interactive, markOpacity, markScale, markX, markY, pinchFocal, pinchFrom, reduceMotion, scrollBy, tapAt, viewport, zoomScale, zoomX, zoomY]);

	const zoomStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: zoomX.value }, { translateY: zoomY.value }, { scale: zoomScale.value }],
	}));
	const markStyle = useAnimatedStyle(() => ({
		opacity: markOpacity.value,
		transform: [{ translateX: markX.value - TOUCH_MARK / 2 }, { translateY: markY.value - TOUCH_MARK / 2 }, { scale: markScale.value }],
	}));
	const onViewportLayout = useCallback((event: LayoutChangeEvent) => {
		const { width, height } = event.nativeEvent.layout;
		setViewportSize({ width, height });
		viewport.value = { width, height };
	}, [viewport]);

	const retry = () => setAttempt((value) => value + 1);
	// The frame stays clear of the bottom toolbar, and notices sit above it.
	const toolbarClearance = toolbarShown ? insets.bottom + bottomToolbarHeight() : 0;

	return <View style={styles.root}>
		{headerItems}
		<View style={[styles.stage, { marginBottom: toolbarClearance }]}>
			<GestureDetector gesture={gesture}>
				<View
					collapsable={false}
					// The stream is a picture of the desktop's page; screen readers
					// hear which page it shows.
					accessible={interactive}
					accessibilityRole="image"
					accessibilityLabel={pageTitle ? `Desktop browser, ${pageTitle}` : "Desktop browser"}
					// Unhandled, VoiceOver's double-tap lands as a touch on the
					// frame's middle and clicks whatever the page has there. Its
					// pass-through gesture still reaches the page by touch.
					onAccessibilityTap={ignoreActivation}
					style={styles.viewport}
					onLayout={onViewportLayout}
				>
					<Animated.View pointerEvents="none" style={[styles.overlay, zoomStyle]}>
						{frameURI && !blank ? <View style={[styles.frame, frameRect]}><Image source={{ uri: frameURI }} resizeMode="contain" style={styles.frameImage} /></View> : null}
					</Animated.View>
					<Animated.View pointerEvents="none" style={[styles.touchMark, markStyle]} />
				</View>
			</GestureDetector>
			{status === "open" && blank ? <View style={styles.overlay}>
				<EmptyState icon="desktop" title="Nothing is open in the desktop browser yet" action={<Button title="Open URL…" icon="globe" onPress={controls.onOpenURL} />} />
			</View> : null}
			{status === "open" && !blank && !frameURI ? <View pointerEvents="none" style={styles.overlay}><EmptyState busy title="Waiting for the desktop browser…" /></View> : null}
			{status !== "open" ? <BrowserState status={status} error={error} retry={retry} /> : null}
			<LoadingBar loading={status === "open" && loading} accessibilityLabel="Loading page" />
		</View>
		{bottomBar}
		<OpenURLPrompt visible={prompting} onSubmit={openURL} onDismiss={closePrompt} />
		<ToastHost toast={toast} bottomOffset={toolbarShown ? toolbarClearance + space.md : undefined} />
	</View>;
}

function BrowserState({ status, error, retry }: { status: BrowserStatus; error?: string; retry(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (status === "connecting") {
		return <View style={[styles.overlay, styles.opaque]}><EmptyState busy title="Connecting to browser…" /></View>;
	}
	return <View accessibilityRole="alert" style={[styles.overlay, styles.opaque]}>
		<EmptyState
			icon="warning"
			iconColor={t.red}
			title="Couldn't connect to the browser"
			message={error || "Couldn't reach the desktop browser. Make sure browser sharing is on in Connect Mobile, the session is still running, and another phone isn't already controlling it."}
			action={<Button title="Check again" icon="refresh" onPress={retry} />}
		/>
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	root: { flex: 1, backgroundColor: t.bgBase },
	stage: { flex: 1 },
	// The letterbox around the fitted frame is the page itself, so it follows the
	// theme instead of drawing black bars in light mode.
	viewport: { flex: 1, overflow: "hidden", backgroundColor: t.bgBase },
	frame: { position: "absolute" },
	frameImage: { width: "100%", height: "100%" },
	overlay: { ...StyleSheet.absoluteFill },
	opaque: { backgroundColor: t.bgBase },
	touchMark: {
		position: "absolute",
		top: 0,
		left: 0,
		width: TOUCH_MARK,
		height: TOUCH_MARK,
		borderRadius: radius.pill,
		borderWidth: space.hair,
		borderColor: t.accent,
		backgroundColor: t.accentPressed,
	},
});
