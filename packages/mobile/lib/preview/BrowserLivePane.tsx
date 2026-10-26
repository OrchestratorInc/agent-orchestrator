import { useEffect, useMemo, useRef, useState } from "react";
import {
	Image,
	PanResponder,
	StyleSheet,
	View,
} from "react-native";
import { BrowserLiveClient } from "../browserLive";
import { browserJPEGDataURI, browserWheelDeltaFromDrag, containBrowserFrame, type BrowserFrameRect } from "../browserLiveProtocol";
import { useApp } from "../store";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { ToastHost } from "../Toast";
import { Button, EmptyState } from "../ui";
import { useToast } from "../useToast";

type BrowserStatus = "connecting" | "open" | "error";

export function BrowserLivePane({ sessionID, onDisabled }: { sessionID: string; onDisabled?(): void }) {
	const { config } = useApp();
	const styles = useThemedStyles(makeStyles);
	const toast = useToast();
	const showNotice = toast.show;
	const client = useRef<BrowserLiveClient | null>(null);
	const [attempt, setAttempt] = useState(0);
	const [status, setStatus] = useState<BrowserStatus>("connecting");
	const [error, setError] = useState<string>();
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
	const [blank, setBlank] = useState(false);

	useEffect(() => {
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
				setBlank(!state.url);
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

	const normalizedPoint = (x: number, y: number) => {
		const rect = frameRectRef.current;
		if (rect.width <= 0 || rect.height <= 0) return null;
		if (x < rect.left || x > rect.left + rect.width || y < rect.top || y > rect.top + rect.height) return null;
		return { x: (x - rect.left) / rect.width, y: (y - rect.top) / rect.height };
	};
	const tap = (x: number, y: number) => {
		const point = normalizedPoint(x, y);
		if (!point) return;
		client.current?.send({ type: "input", payload: { kind: "pointer", phase: "down", ...point, button: "left" } });
		client.current?.send({ type: "input", payload: { kind: "pointer", phase: "up", ...point, button: "left" } });
	};
	const drag = useRef({ x: 0, y: 0, lastDX: 0, lastDY: 0, moved: false });
	const responder = useMemo(() => PanResponder.create({
		onStartShouldSetPanResponder: () => status === "open",
		onMoveShouldSetPanResponder: () => status === "open",
		onPanResponderGrant: (event) => {
			drag.current = { x: event.nativeEvent.locationX, y: event.nativeEvent.locationY, lastDX: 0, lastDY: 0, moved: false };
		},
		onPanResponderMove: (_event, gesture) => {
			if (!drag.current.moved && Math.hypot(gesture.dx, gesture.dy) < 7) return;
			drag.current.moved = true;
			const deltaX = gesture.dx - drag.current.lastDX;
			const deltaY = gesture.dy - drag.current.lastDY;
			drag.current.lastDX = gesture.dx;
			drag.current.lastDY = gesture.dy;
			const scale = frameRectRef.current.width / (pageWidth.current > 1 ? pageWidth.current : frameWidth.current);
			client.current?.send({ type: "input", payload: { kind: "wheel", ...browserWheelDeltaFromDrag(deltaX, deltaY, scale) } });
		},
		onPanResponderRelease: () => {
			if (!drag.current.moved) tap(drag.current.x, drag.current.y);
		},
	}), [status]);

	const retry = () => setAttempt((value) => value + 1);

	return <View style={styles.root}>
		<View
			style={styles.viewport}
			onLayout={(event) => setViewportSize(event.nativeEvent.layout)}
			{...responder.panHandlers}
		>
			{frameURI && !blank ? <View pointerEvents="none" style={[styles.frame, frameRect]}><Image source={{ uri: frameURI }} resizeMode="contain" style={styles.frameImage} /></View> : null}
		</View>
		{status === "open" && blank ? <View pointerEvents="none" style={styles.overlay}><EmptyState icon="desktop" title="Nothing is open in the desktop browser yet" /></View> : null}
		{status === "open" && !blank && !frameURI ? <View pointerEvents="none" style={styles.overlay}><EmptyState busy title="Waiting for the desktop browser…" /></View> : null}
		{status !== "open" ? <BrowserState status={status} error={error} retry={retry} /> : null}
		<ToastHost toast={toast} />
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
	// The letterbox around the fitted frame is the page itself, so it follows the
	// theme instead of drawing black bars in light mode.
	viewport: { flex: 1, overflow: "hidden", backgroundColor: t.bgBase },
	frame: { position: "absolute" },
	frameImage: { width: "100%", height: "100%" },
	overlay: { ...StyleSheet.absoluteFill },
	opaque: { backgroundColor: t.bgBase },
});
