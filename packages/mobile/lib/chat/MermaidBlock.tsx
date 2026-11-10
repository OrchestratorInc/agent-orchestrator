import { memo, useEffect, useMemo, useState, type ReactNode } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import { isRenderableDiagram } from "../../../shared/files/file-formats";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { stayInDiagramPage, useDiagramRemount } from "../MermaidDiagram";
import { mermaidPageHtml, mermaidSvgPageHtml, parseDiagramMessage } from "../mermaidPage";
import { loadMermaidRuntime } from "../mermaidRuntime";
import { cachedDiagram, diagramCacheKey, storeDiagram } from "../mermaidSvgCache";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { radius, space, touchTarget, type } from "../tokens";

// Before the diagram reports its height, the frame holds this much room.
const PLACEHOLDER_HEIGHT = 160;

// A switch segment draws one caption line high; its touch area reaches the
// platform's target, as the code block's own actions do, so a scrolling chat
// neither misses it nor hits it by accident.
const SEGMENT_HEIGHT = type.caption1.lineHeight + space.hair * 2;
const SEGMENT_SLOP = Math.max(0, Math.ceil((touchTarget - SEGMENT_HEIGHT) / 2));
const SEGMENT_HIT_SLOP = { top: SEGMENT_SLOP, bottom: SEGMENT_SLOP, left: space.xxs, right: space.xxs };

/**
 * A ```mermaid fence in a reply or a markdown document, drawn as its diagram,
 * as the desktop draws it (chat/MermaidBlock): a card with the language label
 * and a Diagram / Code switch, so the source is always one tap away. A fence
 * still streaming, one too large or empty, or one the engine cannot draw shows
 * `code` (the ordinary code block) instead — never a blank or error box. The
 * diagram fits the width and does not scroll or zoom inside the message.
 */
export const MermaidBlock = memo(function MermaidBlock({ code, streaming = false, codeBlock }: {
	code: string;
	streaming?: boolean;
	/** The fence as an ordinary code block: the Code view, and the fallback. */
	codeBlock: ReactNode;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const colors = useMemo(() => ({ scheme: scheme === "light" ? "light" as const : "dark" as const, background: t.bgColumn }), [scheme, t.bgColumn]);
	const drawable = !streaming && isRenderableDiagram(code);
	const key = diagramCacheKey(code, colors.scheme);
	const [runtime, setRuntime] = useState<string>();
	const [failed, setFailed] = useState(false);
	const [height, setHeight] = useState<number>();
	const [showCode, setShowCode] = useState(false);
	// A frame whose web process died mounts afresh, from the cached SVG once there is one.
	const page = useDiagramRemount();
	// Read once per source, theme and mount: storing this mount's own SVG must not reload its frame.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	const cached = useMemo(() => drawable ? cachedDiagram(key) : undefined, [drawable, key, page.mount]);
	useEffect(() => {
		if (!drawable || cached || runtime) return;
		let live = true;
		loadMermaidRuntime().then((source) => { if (live) setRuntime(source); }, () => { if (live) setFailed(true); });
		return () => { live = false; };
	}, [cached, drawable, runtime]);
	const html = useMemo(() => {
		if (!drawable) return undefined;
		if (cached) return mermaidSvgPageHtml({ svg: cached, colors, inline: true });
		return runtime ? mermaidPageHtml({ runtime, code, colors, inline: true }) : undefined;
	}, [cached, code, colors, drawable, runtime]);
	// A new source or theme lays out again.
	useEffect(() => {
		setFailed(false);
		setHeight(undefined);
	}, [key]);
	if (!drawable) return <>{codeBlock}</>;
	if (failed) {
		return (
			<View>
				<Text style={styles.note}>Couldn't draw this diagram.</Text>
				{codeBlock}
			</View>
		);
	}
	const onMessage = (event: WebViewMessageEvent) => {
		const message = parseDiagramMessage(event.nativeEvent.data);
		if (message?.type === "failed") setFailed(true);
		else if (message?.type === "rendered") {
			if (message.svg) storeDiagram(key, message.svg);
			if (message.height) setHeight(message.height);
		}
	};
	const toggle = (
		<View accessibilityRole="radiogroup" style={styles.switch}>
			{(["Diagram", "Code"] as const).map((label) => {
				const selected = (label === "Code") === showCode;
				return (
					<Pressable
						key={label}
						accessibilityRole="radio"
						accessibilityState={{ selected }}
						onPress={() => { if (!selected) { haptics.select(); setShowCode(label === "Code"); } }}
						hitSlop={SEGMENT_HIT_SLOP}
						style={[styles.switchItem, selected && styles.switchItemOn]}
					>
						<Text style={[styles.switchText, selected && styles.switchTextOn]}>{label}</Text>
					</Pressable>
				);
			})}
		</View>
	);
	return (
		<View>
			<View style={styles.bar}>{toggle}</View>
			{showCode ? codeBlock : (
				<View style={styles.card}>
					<View style={styles.header}><Text style={styles.label}>mermaid</Text></View>
					<View style={{ height: height ?? PLACEHOLDER_HEIGHT }}>
						{html ? (
							<WebView
								key={page.mount}
								source={{ html }}
								originWhitelist={["*"]}
								onShouldStartLoadWithRequest={stayInDiagramPage}
								onMessage={onMessage}
								onError={() => setFailed(true)}
								// iOS ends WKWebView content processes in the background; Android can lose the renderer.
								onContentProcessDidTerminate={() => page.gone(() => setFailed(true))}
								onRenderProcessGone={() => page.gone(() => setFailed(true))}
								scrollEnabled={false}
								javaScriptEnabled
								allowFileAccess={false}
								setSupportMultipleWindows={false}
								accessibilityLabel="Mermaid diagram"
								style={[styles.web, height === undefined && styles.hidden]}
							/>
						) : null}
						{height === undefined ? <View style={styles.loading}><ActivityIndicator color={t.accent} /></View> : null}
					</View>
				</View>
			)}
		</View>
	);
});

const makeStyles = (t: Theme) => StyleSheet.create({
	bar: { flexDirection: "row", justifyContent: "flex-end", marginBottom: space.xs },
	switch: { flexDirection: "row", padding: 2, borderRadius: radius.sm, borderCurve: "continuous", borderWidth: rowDividerWidth, borderColor: t.borderSubtle, backgroundColor: t.bgBase },
	switchItem: { paddingHorizontal: space.sm, paddingVertical: space.hair, borderRadius: radius.sm - 2, borderCurve: "continuous" },
	switchItemOn: { backgroundColor: t.bgElevated },
	switchText: { ...type.caption1, color: t.textTertiary },
	switchTextOn: { color: t.textPrimary },
	card: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden", borderWidth: rowDividerWidth, borderColor: t.borderSubtle, backgroundColor: t.bgColumn },
	header: { paddingHorizontal: space.md, paddingVertical: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	label: { color: t.textTertiary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
	web: { flex: 1, backgroundColor: t.bgColumn },
	hidden: { opacity: 0 },
	loading: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "center", justifyContent: "center" },
	note: { ...type.caption1, color: t.textTertiary, marginBottom: space.xs },
});
