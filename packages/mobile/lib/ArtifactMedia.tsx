import { useEvent } from "expo";
import { useVideoPlayer, VideoView, type VideoPlayer } from "expo-video";
import { useEffect, useRef, useState } from "react";
import { AccessibilityInfo, ActivityIndicator, Platform, Pressable, StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { haptics } from "./haptics";
import { formatMediaTime } from "./mediaTime";
import { Feather } from "./icons";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, radius, space, type } from "./tokens";
import { EmptyState } from "./ui";
import { useGlassBarInset } from "./useHeaderOverlap";

// How long a failed Cloud source waits for its renewed address before saying it can't play.
const RENEW_WAIT_MS = 10_000;
// How long a playing video's controls stay up after they were last used.
const CONTROLS_HIDE_MS = 3_000;
// Video controls sit over the picture, whatever the app's theme.
const ON_VIDEO = { glyph: "#FFFFFF", text: "rgba(255,255,255,0.85)", button: "rgba(0,0,0,0.5)", bar: "rgba(0,0,0,0.6)", track: "rgba(255,255,255,0.3)" };

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
		// The app draws the position for audio, and for video on iOS (see VideoControls).
		if (kind === "audio" || Platform.OS === "ios") created.timeUpdateEventInterval = 0.25;
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
			{Platform.OS === "ios"
				? <VideoControls key={source.uri} player={player} ready={status === "readyToPlay"} />
				: <VideoView player={player} nativeControls contentFit="contain" fullscreenOptions={{ enable: true }} allowsPictureInPicture={false} style={styles.video} />}
		</View>
	);
}

/** Play and pause, and where the file is, for the controls the app draws itself. */
function useTransport(player: VideoPlayer) {
	const { isPlaying } = useEvent(player, "playingChange", { isPlaying: player.playing });
	const { currentTime } = useEvent(player, "timeUpdate", { currentTime: player.currentTime, currentLiveTimestamp: null, currentOffsetFromLive: null, bufferedPosition: 0 });
	const { duration } = useEvent(player, "sourceLoad", { duration: player.duration, videoSource: null, availableVideoTracks: [], availableSubtitleTracks: [], availableAudioTracks: [] });
	const length = duration > 0 ? duration : player.duration;
	const toggle = () => {
		haptics.tap();
		if (isPlaying) player.pause();
		else {
			// Finished: start over, as a media element's play button does.
			if (length > 0 && currentTime >= length - 0.25) player.currentTime = 0;
			player.play();
		}
	};
	return { isPlaying, currentTime, length, toggle };
}

function useScreenReaderEnabled(): boolean {
	const [enabled, setEnabled] = useState(false);
	useEffect(() => {
		let live = true;
		void AccessibilityInfo.isScreenReaderEnabled().then((value) => { if (live) setEnabled(value); }, () => {});
		const subscription = AccessibilityInfo.addEventListener("screenReaderChanged", setEnabled);
		return () => { live = false; subscription.remove(); };
	}, []);
	return enabled;
}

/**
 * A video's controls on iOS, drawn by the app. The platform player's own
 * controls never came up in the artifact viewer on iOS 27, even when tapped,
 * and on iOS 26 a video not yet played showed none until the first tap, so a
 * paused video was a bare picture with no way to see it could play. A tap on
 * the picture shows or hides these; they hide while playing and stay while
 * paused (or while a screen reader is on). Fullscreen is the platform's
 * player, presented with its own controls.
 */
function VideoControls({ player, ready }: { player: VideoPlayer; ready: boolean }) {
	const styles = useThemedStyles(makeStyles);
	const view = useRef<VideoView>(null);
	const { isPlaying, currentTime, length, toggle } = useTransport(player);
	const screenReader = useScreenReaderEnabled();
	const [shown, setShown] = useState(true);
	// Bumped by each use of the controls, so the hide waits from the last one.
	const [used, setUsed] = useState(0);
	const [fullscreen, setFullscreen] = useState(false);
	useEffect(() => {
		if (!isPlaying) setShown(true);
	}, [isPlaying]);
	useEffect(() => {
		if (!shown || !isPlaying || screenReader) return;
		const timer = setTimeout(() => setShown(false), CONTROLS_HIDE_MS);
		return () => clearTimeout(timer);
	}, [shown, isPlaying, screenReader, used]);
	const touched = () => setUsed((n) => n + 1);
	const enterFullscreen = () => {
		haptics.tap();
		// Fullscreen shows the platform's controls; they are turned on first so it opens with them.
		setFullscreen(true);
		requestAnimationFrame(() => {
			view.current?.enterFullscreen().catch(() => setFullscreen(false));
		});
	};
	return (
		<View style={styles.video}>
			<VideoView
				ref={view}
				player={player}
				nativeControls={fullscreen}
				contentFit="contain"
				fullscreenOptions={{ enable: true }}
				allowsPictureInPicture={false}
				onFullscreenEnter={() => setFullscreen(true)}
				onFullscreenExit={() => setFullscreen(false)}
				style={StyleSheet.absoluteFill}
			/>
			{/* Only playback hides the controls: a tap on a paused picture leaves them up. */}
			<Pressable accessible={false} onPress={() => { if (isPlaying) setShown((value) => !value); }} style={StyleSheet.absoluteFill} />
			{(shown || screenReader) && !fullscreen ? (
				<View pointerEvents="box-none" style={StyleSheet.absoluteFill}>
					<View pointerEvents="box-none" style={styles.videoCenter}>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel={isPlaying ? "Pause" : "Play"}
							disabled={!ready}
							onPress={() => { toggle(); touched(); }}
							style={({ pressed }) => [styles.videoPlay, pressed && styles.pressed, !ready && styles.disabled]}
						>
							<Feather name={isPlaying ? "pause" : "play"} size={iconSize.xl} color={ON_VIDEO.glyph} />
						</Pressable>
					</View>
					{/* With no filmstrip the page runs to the screen's bottom edge: keep the bar above the home indicator. */}
					<SafeAreaView edges={["bottom"]}>
						<View style={styles.videoBar}>
							<View style={styles.videoPosition}>
								<MediaPosition player={player} currentTime={currentTime} length={length} onVideo onSeek={touched} />
							</View>
							<Pressable
								accessibilityRole="button"
								accessibilityLabel="Full screen"
								disabled={!ready}
								onPress={enterFullscreen}
								hitSlop={space.sm}
								style={({ pressed }) => [styles.videoFullscreen, pressed && styles.pressed, !ready && styles.disabled]}
							>
								<Feather name="maximize-2" size={iconSize.md} color={ON_VIDEO.glyph} />
							</Pressable>
						</View>
					</SafeAreaView>
				</View>
			) : null}
		</View>
	);
}

/** A bar that shows the position and seeks where it is tapped, over the elapsed and total times. */
function MediaPosition({ player, currentTime, length, onVideo = false, onSeek }: { player: VideoPlayer; currentTime: number; length: number; onVideo?: boolean; onSeek?: () => void }) {
	const styles = useThemedStyles(makeStyles);
	const [barWidth, setBarWidth] = useState(0);
	const progress = length > 0 ? Math.min(1, Math.max(0, currentTime / length)) : 0;
	return (
		<>
			<Pressable
				accessibilityRole="adjustable"
				accessibilityLabel="Position"
				accessibilityValue={{ text: `${formatMediaTime(currentTime)} of ${formatMediaTime(length)}` }}
				accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
				onAccessibilityAction={(event) => { player.seekBy(event.nativeEvent.actionName === "increment" ? 10 : -10); onSeek?.(); }}
				onLayout={(event: LayoutChangeEvent) => setBarWidth(event.nativeEvent.layout.width)}
				onPress={(event) => { if (barWidth > 0 && length > 0) player.currentTime = (event.nativeEvent.locationX / barWidth) * length; onSeek?.(); }}
				hitSlop={{ top: space.md, bottom: space.md }}
				style={[styles.track, onVideo && styles.trackOnVideo]}
			>
				<View style={[styles.trackFill, onVideo && styles.trackFillOnVideo, { width: `${progress * 100}%` }]} />
			</Pressable>
			<View style={[styles.times, onVideo && styles.timesOnVideo]}>
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.time, onVideo && styles.timeOnVideo]}>{formatMediaTime(currentTime)}</Text>
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.time, onVideo && styles.timeOnVideo]}>{formatMediaTime(length)}</Text>
			</View>
		</>
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
	const { isPlaying, currentTime, length, toggle } = useTransport(player);
	return (
		<View style={[styles.screen, { paddingTop: glassInset }]}>
			<View style={styles.audioArt}>
				<View style={styles.audioGlyph}><Feather name="music" size={iconSize.hero} color={t.textTertiary} /></View>
				<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={2} style={styles.audioName}>{name}</Text>
			</View>
			<View style={styles.audioControls}>
				<MediaPosition player={player} currentTime={currentTime} length={length} />
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
	trackOnVideo: { backgroundColor: ON_VIDEO.track },
	trackFillOnVideo: { backgroundColor: ON_VIDEO.glyph },
	timesOnVideo: { marginBottom: 0 },
	timeOnVideo: { color: ON_VIDEO.text },
	videoCenter: { flex: 1, alignItems: "center", justifyContent: "center" },
	videoPlay: { width: 64, height: 64, borderRadius: 32, alignItems: "center", justifyContent: "center", backgroundColor: ON_VIDEO.button },
	videoBar: { flexDirection: "row", alignItems: "center", gap: space.md, marginHorizontal: space.md, marginBottom: space.md, paddingHorizontal: space.lg, paddingTop: space.md, paddingBottom: space.sm, borderRadius: radius.lg, borderCurve: "continuous", backgroundColor: ON_VIDEO.bar },
	videoPosition: { flex: 1 },
	videoFullscreen: { width: 32, height: 32, alignItems: "center", justifyContent: "center", marginTop: -space.xs },
	pressed: { opacity: 0.7 },
	disabled: { opacity: 0.4 },
});
