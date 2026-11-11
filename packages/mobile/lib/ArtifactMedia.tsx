import { useEvent } from "expo";
import { useVideoPlayer, VideoView, type VideoPlayer } from "expo-video";
import { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import { haptics } from "./haptics";
import { formatMediaTime } from "./mediaTime";
import { Feather } from "./icons";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, space, type } from "./tokens";
import { EmptyState } from "./ui";
import { useGlassBarInset } from "./useHeaderOverlap";

// How long a failed Cloud source waits for its renewed address before saying it can't play.
const RENEW_WAIT_MS = 10_000;

/**
 * A video or audio artifact in the platform's own player, as the desktop plays
 * it in a media element. The player sends the connection's credential with
 * every request it makes, ranges included: a WebView sends it only with the
 * page load, so its player's own requests were refused. A page swiped off
 * screen pauses, and a neighbouring page not yet shown loads nothing.
 *
 * A Cloud source is a signed address that expires: a player that fails asks
 * for fresh access once (`renew`) and plays from the new address, and says it
 * can't play only if that fails too, as the web viewer does.
 */
export function ArtifactMedia({ source, kind, active, name, label, hint, renew }: {
	/** A daemon's source carries the credential; Cloud's signed address needs none. */
	source: { uri: string; headers?: Record<string, string> };
	kind: "video" | "audio";
	/** Whether this is the page on screen. */
	active: boolean;
	name: string;
	/** What the file is, for when it cannot play: "WEBM file · 4.1 MB". */
	label: string;
	/** Where else it can be opened, said with the format when it cannot play here. */
	hint?: string;
	/** Asks for fresh access after a refused load (Cloud's signed address ran out). */
	renew?: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const glassInset = useGlassBarInset();
	// The gallery mounts the pages beside the one on screen: until a page is
	// shown, its player has no file, so nothing is buffered for a video not opened.
	const [shown, setShown] = useState(active);
	useEffect(() => {
		if (active) setShown(true);
	}, [active]);
	const player = useVideoPlayer(shown ? source : null, (created) => {
		created.loop = false;
		// Audio draws its own position; a video's controls draw theirs.
		if (kind === "audio") created.timeUpdateEventInterval = 0.25;
	});
	const { status } = useEvent(player, "statusChange", { status: player.status });
	useEffect(() => {
		if (!active) player.pause();
	}, [active, player]);
	const failed = status === "error";
	// The address the player failed on, when fresh access was asked for.
	const [renewedFrom, setRenewedFrom] = useState<string>();
	const [renewTimedOut, setRenewTimedOut] = useState(false);
	useEffect(() => {
		if (!failed || !renew || renewedFrom !== undefined) return;
		setRenewedFrom(source.uri);
		renew();
	}, [failed, renew, renewedFrom, source.uri]);
	// Still on the address that failed, within the wait: the renewed one is on its way.
	const awaitingRenewal = failed && Boolean(renew) && (renewedFrom === undefined || renewedFrom === source.uri) && !renewTimedOut;
	useEffect(() => {
		if (!awaitingRenewal) return;
		const timer = setTimeout(() => setRenewTimedOut(true), RENEW_WAIT_MS);
		return () => clearTimeout(timer);
	}, [awaitingRenewal]);
	if (awaitingRenewal) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (failed) {
		return <View style={styles.center}><EmptyState icon={kind === "video" ? { ios: "video.slash", android: "videocam_off" } : { ios: "speaker.slash", android: "music_off" }} title={kind === "video" ? "This video can't play on the phone" : "This audio can't play on the phone"} message={hint ? `${label}\n${hint}` : label} /></View>;
	}
	// A rewritten file is a new source: fresh controls, so none of the last player's state (playing) carries over.
	if (kind === "audio") return <AudioPlayer key={source.uri} player={player} name={name} ready={status === "readyToPlay"} />;
	return (
		<View style={[styles.screen, { paddingTop: glassInset }]}>
			<VideoView player={player} nativeControls contentFit="contain" fullscreenOptions={{ enable: true }} allowsPictureInPicture={false} style={styles.video} />
		</View>
	);
}

/**
 * Audio has no picture for the platform's player to put its controls over (an
 * audio-only player view shows none at all), so it gets its own: play and
 * pause, and a bar that shows the position and seeks where it is tapped.
 */
function AudioPlayer({ player, name, ready }: { player: VideoPlayer; name: string; ready: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const glassInset = useGlassBarInset();
	const { isPlaying } = useEvent(player, "playingChange", { isPlaying: player.playing });
	const { currentTime } = useEvent(player, "timeUpdate", { currentTime: player.currentTime, currentLiveTimestamp: null, currentOffsetFromLive: null, bufferedPosition: 0 });
	const { duration } = useEvent(player, "sourceLoad", { duration: player.duration, videoSource: null, availableVideoTracks: [], availableSubtitleTracks: [], availableAudioTracks: [] });
	const length = duration > 0 ? duration : player.duration;
	const [barWidth, setBarWidth] = useState(0);
	const progress = length > 0 ? Math.min(1, Math.max(0, currentTime / length)) : 0;
	const toggle = () => {
		haptics.tap();
		if (isPlaying) player.pause();
		else {
			// Finished: start over, as a media element's play button does.
			if (length > 0 && currentTime >= length - 0.25) player.currentTime = 0;
			player.play();
		}
	};
	return (
		<View style={[styles.screen, { paddingTop: glassInset }]}>
			<View style={styles.audioArt}>
				<View style={styles.audioGlyph}><Feather name="music" size={iconSize.hero} color={t.textTertiary} /></View>
				<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={2} style={styles.audioName}>{name}</Text>
			</View>
			<View style={styles.audioControls}>
				<Pressable
					accessibilityRole="adjustable"
					accessibilityLabel="Position"
					accessibilityValue={{ text: `${formatMediaTime(currentTime)} of ${formatMediaTime(length)}` }}
					accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
					onAccessibilityAction={(event) => player.seekBy(event.nativeEvent.actionName === "increment" ? 10 : -10)}
					onLayout={(event: LayoutChangeEvent) => setBarWidth(event.nativeEvent.layout.width)}
					onPress={(event) => { if (barWidth > 0 && length > 0) player.currentTime = (event.nativeEvent.locationX / barWidth) * length; }}
					hitSlop={{ top: space.md, bottom: space.md }}
					style={styles.track}
				>
					<View style={[styles.trackFill, { width: `${progress * 100}%` }]} />
				</Pressable>
				<View style={styles.times}>
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.time}>{formatMediaTime(currentTime)}</Text>
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.time}>{formatMediaTime(length)}</Text>
				</View>
				<Pressable
					accessibilityRole="button"
					accessibilityLabel={isPlaying ? "Pause" : "Play"}
					disabled={!ready}
					onPress={toggle}
					style={({ pressed }) => [styles.playButton, pressed && styles.pressed, !ready && styles.disabled]}
				>
					<Feather name={isPlaying ? "pause" : "play"} size={iconSize.xl} color={t.onAccent} />
				</Pressable>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	video: { flex: 1, backgroundColor: t.bgBase },
	audioArt: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.xl },
	audioGlyph: { width: 120, height: 120, borderRadius: 24, borderCurve: "continuous", alignItems: "center", justifyContent: "center", backgroundColor: t.bgElevated },
	audioName: { ...type.body, color: t.textPrimary, textAlign: "center" },
	audioControls: { paddingHorizontal: space.xl, paddingBottom: space.xxxl, alignItems: "center" },
	track: { alignSelf: "stretch", height: 4, borderRadius: 2, backgroundColor: t.borderDefault, overflow: "hidden" },
	trackFill: { height: "100%", backgroundColor: t.accent },
	times: { alignSelf: "stretch", flexDirection: "row", justifyContent: "space-between", marginTop: space.xs, marginBottom: space.lg },
	time: { ...type.caption1, color: t.textSecondary, fontVariant: ["tabular-nums"] },
	playButton: { width: 64, height: 64, borderRadius: 32, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	pressed: { opacity: 0.7 },
	disabled: { opacity: 0.4 },
});
