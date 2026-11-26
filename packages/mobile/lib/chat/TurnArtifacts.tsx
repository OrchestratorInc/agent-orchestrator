import { useRouter } from "expo-router";
import { useIsFocused } from "expo-router/react-navigation";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { turnArtifactVerb, type TurnArtifact } from "../../../shared/artifacts/turn-artifacts";
import { ArtifactPoster } from "../ArtifactPoster";
import type { SessionArtifact } from "../artifacts";
import type { ArtifactsBackend } from "../artifactsBackend";
import { haptics } from "../haptics";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, press, radius, space, type } from "../tokens";
import { useArtifactsBackend } from "../useArtifactsBackend";
import { useSessionArtifacts } from "../useSessionArtifacts";
import { renderFrameLive, subscribeRenderViewport } from "./renderFrameModel";
import { turnsTouchArtifacts } from "./turnArtifactsModel";
import type { ConversationTurn } from "./types";

/** What a session's turns need to show the artifacts they made. */
export type TurnArtifactSource = {
	sessionId: string;
	backend: ArtifactsBackend | null;
	/** Every file in the session's artifact directory; undefined until read. */
	artifacts?: SessionArtifact[];
	versionOf(path: string): string | undefined;
	open(path: string): void;
};

export const TurnArtifactsContext = createContext<TurnArtifactSource | null>(null);

export function useTurnArtifactSource(): TurnArtifactSource | null {
	return useContext(TurnArtifactsContext);
}

/** The artifact source a session screen hands its timeline. */
export function useTurnArtifactSourceFor(sessionId: string, turns: readonly ConversationTurn[] | undefined, hostId?: string): TurnArtifactSource | null {
	const router = useRouter();
	const wanted = turnsTouchArtifacts(turns, sessionId);
	const { backend } = useArtifactsBackend(wanted ? sessionId : "");
	const { artifacts } = useSessionArtifacts(wanted ? sessionId : "", backend);
	const versionOf = useCallback((path: string) => artifacts?.find((file) => file.path === path)?.updatedAt, [artifacts]);
	const open = useCallback((path: string) => {
		haptics.tap();
		router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, path, hostId } });
	}, [hostId, router, sessionId]);
	return useMemo(() => wanted ? { sessionId, backend, artifacts, versionOf, open } : null, [artifacts, backend, open, sessionId, versionOf, wanted]);
}

// Small square tiles in one row, so a turn's artifacts take a line of the
// thread rather than a screen of it; more than fit scroll sideways.
const TILE = 72;
const TILE_GAP = space.sm;

/**
 * The deliverables one turn created or edited, as small preview tiles under
 * the reply: the desktop's turn artifacts, sized for a phone. A tile opens the
 * artifact.
 */
export function TurnArtifacts({ artifacts, source }: { artifacts: TurnArtifact<SessionArtifact>[]; source: TurnArtifactSource }) {
	const styles = useThemedStyles(makeStyles);
	const { ref, visible } = useInChatView();
	const label = artifacts.length === 1 ? "1 Artifact" : `${artifacts.length} Artifacts`;
	return (
		<View ref={ref} style={styles.section} accessibilityLabel={label}>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.label}>{label}</Text>
			<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.row} style={styles.strip}>
				{artifacts.map(({ artifact, status }) => {
					const verb = turnArtifactVerb(status);
					return (
						<Pressable
							key={artifact.path}
							accessibilityRole="button"
							accessibilityLabel={`Open ${artifact.name}, ${verb.toLowerCase()} in this turn`}
							onPress={() => source.open(artifact.path)}
							style={styles.tile}
						>
							{({ pressed }) => <>
								<View style={styles.frame}>
									<ArtifactPoster artifact={artifact} backend={source.backend} sessionId={source.sessionId} visible={visible} versionOf={source.versionOf} iconSize={18} style={styles.poster} />
									{/* A veil, not opacity: an Android WebView does not fade with its parent. */}
									{pressed ? <View pointerEvents="none" style={styles.pressed} /> : null}
								</View>
								<View>
									{/* Cut at the end: the start is what tells similar names apart, and a
									    tile with no preview shows its extension on the icon. */}
									<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.name}>{artifact.name}</Text>
									<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.verb}>{verb}</Text>
								</View>
							</>}
						</Pressable>
					);
				})}
			</ScrollView>
		</View>
	);
}

/**
 * Whether the cards are near the screen, the way the chat's render frames
 * decide: their page previews load in range and let go well past it, and
 * while another screen covers the chat.
 */
function useInChatView() {
	const ref = useRef<View>(null);
	const { height: viewportHeight } = useWindowDimensions();
	const focused = useIsFocused();
	const [visible, setVisible] = useState(false);
	const state = useRef({ visible, focused });
	state.current = { visible, focused };
	const measure = useCallback(() => {
		ref.current?.measureInWindow((_x, y, _width, height) => {
			// A cell the list has clipped measures as zero; leave it as it was.
			if (height <= 0) return;
			const next = state.current.focused && renderFrameLive(y, height, viewportHeight, state.current.visible);
			if (next !== state.current.visible) setVisible(next);
		});
	}, [viewportHeight]);
	useEffect(() => subscribeRenderViewport(measure), [measure]);
	useEffect(() => { measure(); }, [focused, measure]);
	return { ref, visible };
}

const makeStyles = (t: Theme) => StyleSheet.create({
	section: { gap: space.xs + 2 },
	label: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	// Bleeds to the screen edge while scrolling, so a clipped tile reads as "more".
	strip: { marginHorizontal: -space.xl },
	row: { gap: TILE_GAP, paddingHorizontal: space.xl },
	tile: { width: TILE, gap: space.xs },
	frame: { overflow: "hidden", borderRadius: radius.sm, borderCurve: "continuous", borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	poster: { width: TILE - 2 * StyleSheet.hairlineWidth, aspectRatio: 1 },
	pressed: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase, opacity: 1 - press.opacity },
	name: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	verb: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
});
