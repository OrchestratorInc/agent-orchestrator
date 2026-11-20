import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Check, Maximize2, Minimize2, Pause, Play, Repeat, Volume2, VolumeX } from "@/components/app-icons";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "./ui/option-menu";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";

export const PLAYBACK_SPEEDS = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2] as const;
const SKIP_SECONDS = 5;
const VOLUME_STEP = 0.05;
/** Controls a narrow player leaves out; see the controls row. */
const COMPACT_HIDDEN = "@max-[26rem]:hidden";

/** `m:ss`, or `h:mm:ss` once the time passes an hour. Unknown times read 0:00. */
export function formatMediaTime(seconds: number): string {
	if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
	const total = Math.floor(seconds);
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const secs = String(total % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${secs}` : `${minutes}:${secs}`;
}

/** How far the file has loaded past the playhead, in seconds. */
function bufferedEnd(video: HTMLVideoElement): number {
	const ranges = video.buffered;
	for (let index = 0; index < ranges.length; index++) {
		if (ranges.start(index) <= video.currentTime && video.currentTime <= ranges.end(index)) return ranges.end(index);
	}
	return 0;
}

/** Plays a video with the app's own controls instead of Chromium's. */
export function VideoPlayer({ active = true, src, label, crossOrigin, onError, className }: {
	/**
	 * False while the player is mounted in a hidden pane, such as an inactive file
	 * tab. It then does not autoplay, and pauses if it was playing.
	 */
	active?: boolean;
	src: string;
	/** Accessible name of the video itself, usually its path. */
	label: string;
	crossOrigin?: "anonymous" | "use-credentials";
	onError?: () => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const rootRef = useRef<HTMLDivElement>(null);
	const videoRef = useRef<HTMLVideoElement>(null);
	// What playback looked like before the latest single click on the video, so
	// a double-click can put it back.
	const beforeClick = useRef<{ paused: boolean; time: number } | null>(null);
	const [playing, setPlaying] = useState(false);
	const [time, setTime] = useState(0);
	const [duration, setDuration] = useState(0);
	const [buffered, setBuffered] = useState(0);
	const [muted, setMuted] = useState(false);
	const [volume, setVolume] = useState(1);
	const [loop, setLoop] = useState(false);
	const [speed, setSpeed] = useState(1);
	const [fullscreen, setFullscreen] = useState(false);
	// A new file version gets a fresh element (see the video's key) that starts
	// from the beginning; loop, speed, and volume carry over to it.
	const [shownSrc, setShownSrc] = useState(src);
	if (shownSrc !== src) {
		setShownSrc(src);
		setPlaying(false);
		setTime(0);
		setDuration(0);
		setBuffered(0);
	}
	const seekable = Number.isFinite(duration) && duration > 0;
	// Menus and tooltips portal to <body>, which a fullscreen element hides.
	const portal = fullscreen ? rootRef.current : undefined;

	// Loading resets the element's rate to its default, so set both.
	useEffect(() => {
		const video = videoRef.current;
		if (!video) return;
		video.defaultPlaybackRate = speed;
		video.playbackRate = speed;
	}, [speed, src]);

	// Hidden panes stay mounted, so only the visible one may play: pause when the
	// pane is hidden, and give a video that has never played its autoplay the
	// first time its pane is shown. One the person paused stays paused.
	const wasActive = useRef(active);
	useEffect(() => {
		const shown = active && !wasActive.current;
		wasActive.current = active;
		const video = videoRef.current;
		if (!video) return;
		if (!active) {
			if (!video.paused) video.pause();
		} else if (shown && video.paused && (video.played?.length ?? 0) === 0) void Promise.resolve(video.play()).catch(() => {});
	}, [active]);

	useEffect(() => {
		const video = videoRef.current;
		if (!video) return;
		video.volume = volume;
		video.muted = muted;
	}, [muted, volume, src]);

	// timeupdate fires a few times a second; follow the frame clock while
	// playing so the scrubber moves smoothly.
	useEffect(() => {
		if (!playing) return;
		let frame = 0;
		const tick = () => {
			const video = videoRef.current;
			if (video) setTime(video.currentTime);
			frame = requestAnimationFrame(tick);
		};
		frame = requestAnimationFrame(tick);
		return () => cancelAnimationFrame(frame);
	}, [playing]);

	useEffect(() => {
		const sync = () => setFullscreen(document.fullscreenElement !== null && document.fullscreenElement === rootRef.current);
		document.addEventListener("fullscreenchange", sync);
		return () => document.removeEventListener("fullscreenchange", sync);
	}, []);

	const togglePlay = useCallback(() => {
		const video = videoRef.current;
		if (!video) return;
		// play() rejects when a pause or new source interrupts it; that is not an error.
		if (video.paused || video.ended) void Promise.resolve(video.play()).catch(() => {});
		else video.pause();
	}, []);

	const seek = useCallback((to: number) => {
		const video = videoRef.current;
		if (!video || !Number.isFinite(video.duration)) return;
		const next = Math.min(Math.max(to, 0), video.duration);
		video.currentTime = next;
		setTime(next);
	}, []);

	const skip = useCallback((by: number) => {
		const video = videoRef.current;
		if (video) seek(video.currentTime + by);
	}, [seek]);

	const setLevel = useCallback((level: number) => {
		const video = videoRef.current;
		if (!video) return;
		video.volume = Math.min(Math.max(level, 0), 1);
		video.muted = video.volume === 0;
	}, []);

	const toggleMute = useCallback(() => {
		const video = videoRef.current;
		if (!video) return;
		if (video.muted || video.volume === 0) {
			if (video.volume === 0) video.volume = 1;
			video.muted = false;
		} else {
			video.muted = true;
		}
	}, []);

	const fullscreenAvailable = typeof document !== "undefined" && document.fullscreenEnabled === true;
	const toggleFullscreen = useCallback(() => {
		if (!document.fullscreenEnabled) return;
		if (document.fullscreenElement) void Promise.resolve(document.exitFullscreen()).catch(() => {});
		else void Promise.resolve(rootRef.current?.requestFullscreen()).catch(() => {});
	}, []);

	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
		const target = event.target as HTMLElement;
		// Keys typed in the speed menu bubble here through its portal, which sits
		// inside the player while it is fullscreen; leave them to the menu.
		if (!rootRef.current?.contains(target) || target.closest('[role="menu"]')) return;
		const onButton = target.closest("button") !== null;
		const onVolume = target.dataset.videoVolume !== undefined;
		const key = event.key.toLowerCase();
		if ((key === " " && !onButton) || key === "k") togglePlay();
		else if (key === "arrowleft" && !onVolume) skip(-SKIP_SECONDS);
		else if (key === "arrowright" && !onVolume) skip(SKIP_SECONDS);
		else if (key === "m") toggleMute();
		else if (key === "f") toggleFullscreen();
		else return;
		event.preventDefault();
	};

	const level = muted ? 0 : volume;
	return (
		<div
			ref={rootRef}
			data-video-player=""
			onKeyDown={onKeyDown}
			className={cn("@container flex size-full min-h-0 min-w-0 flex-col [&:fullscreen]:bg-background [&:fullscreen]:p-3", className)}
		>
			<video
				ref={videoRef}
				aria-label={label}
				autoPlay={active}
				className="min-h-0 w-full flex-1 cursor-pointer rounded-md object-contain outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
				crossOrigin={crossOrigin}
				key={src}
				loop={loop}
				onClick={(event) => {
					// A single click toggles at once; the second click of a double-click
					// leaves playback to the double-click handler.
					if (event.detail > 1) return;
					const video = event.currentTarget;
					beforeClick.current = { paused: video.paused || video.ended, time: video.currentTime };
					togglePlay();
				}}
				onDoubleClick={(event) => {
					// The browser recognizes a double-click at the system's own speed, after
					// its first click already toggled playback: undo that, then go fullscreen.
					const before = beforeClick.current;
					beforeClick.current = null;
					const video = event.currentTarget;
					if (before?.paused) {
						video.pause();
						video.currentTime = before.time;
						setTime(before.time);
					} else if (before && video.paused) {
						void Promise.resolve(video.play()).catch(() => {});
					}
					toggleFullscreen();
				}}
				onDurationChange={(event) => setDuration(event.currentTarget.duration)}
				onEnded={() => setPlaying(false)}
				onError={onError}
				onLoadedMetadata={(event) => setDuration(event.currentTarget.duration)}
				onPause={() => setPlaying(false)}
				onPlay={() => setPlaying(true)}
				onProgress={(event) => setBuffered(bufferedEnd(event.currentTarget))}
				onTimeUpdate={(event) => {
					setTime(event.currentTarget.currentTime);
					setBuffered(bufferedEnd(event.currentTarget));
				}}
				onVolumeChange={(event) => {
					setMuted(event.currentTarget.muted);
					setVolume(event.currentTarget.volume);
				}}
				playsInline
				preload="metadata"
				src={src}
				tabIndex={0}
			/>
			<TooltipProvider>
				<div className="flex shrink-0 flex-col gap-1 pt-2">
					<MediaSlider
						disabled={!seekable}
						label={t("files.video.seek")}
						max={seekable ? duration : 0}
						onChange={seek}
						secondary={seekable ? buffered : 0}
						step="any"
						value={seekable ? Math.min(time, duration) : 0}
						valueText={t("files.video.time", { current: formatMediaTime(time), duration: formatMediaTime(duration) })}
					/>
					{/* Narrow previews, such as a 300 px inspector, drop the volume
					    slider (mute still covers it); the row wraps rather than clip if it
					    is narrower still. ←/→ skip 5 s; there are no skip buttons. */}
					<div className="flex min-w-0 flex-wrap items-center gap-1">
						<ControlButton label={playing ? t("files.video.pause") : t("files.video.play")} onClick={togglePlay} portal={portal}>
							{playing ? <Pause /> : <Play />}
						</ControlButton>
						<ControlButton label={level === 0 ? t("files.video.unmute") : t("files.video.mute")} onClick={toggleMute} portal={portal}>
							{level === 0 ? <VolumeX /> : <Volume2 />}
						</ControlButton>
						<MediaSlider
							className={cn("w-16 shrink-0", COMPACT_HIDDEN)}
							label={t("files.video.volume")}
							max={1}
							onChange={setLevel}
							step={VOLUME_STEP}
							value={level}
							valueText={`${Math.round(level * 100)}%`}
							volume
						/>
						<span className="ml-auto min-w-0 truncate pr-1 text-caption text-muted-foreground tabular-nums">
							{t("files.video.time", { current: formatMediaTime(time), duration: formatMediaTime(duration) })}
						</span>
						<OptionMenu>
							<OptionMenuTrigger
								aria-label={t("files.video.speed")}
								className="h-7 shrink-0 gap-1 rounded-md px-2 text-caption text-muted-foreground tabular-nums hover:text-foreground data-[state=open]:text-foreground"
								variant="ghost"
							>
								{t("files.video.speedValue", { speed })}
							</OptionMenuTrigger>
							<OptionMenuContent align="end" className="min-w-0 w-28" container={portal} side="top">
								{PLAYBACK_SPEEDS.map((choice) => (
									<OptionMenuItem active={choice === speed} key={choice} onSelect={() => setSpeed(choice)} radio>
										<span className="flex-1 tabular-nums">{t("files.video.speedValue", { speed: choice })}</span>
										<Check aria-hidden="true" className={cn("size-3 shrink-0", choice !== speed && "invisible")} />
									</OptionMenuItem>
								))}
							</OptionMenuContent>
						</OptionMenu>
						<ControlButton label={t("files.video.loop")} onClick={() => setLoop((on) => !on)} portal={portal} pressed={loop}>
							<Repeat />
						</ControlButton>
						{fullscreenAvailable ? (
							<ControlButton label={fullscreen ? t("files.video.exitFullscreen") : t("files.video.fullscreen")} onClick={toggleFullscreen} portal={portal}>
								{fullscreen ? <Minimize2 /> : <Maximize2 />}
							</ControlButton>
						) : null}
					</div>
				</div>
			</TooltipProvider>
		</div>
	);
}

function ControlButton({ children, className, disabled, label, onClick, portal, pressed }: {
	children: ReactNode;
	className?: string;
	disabled?: boolean;
	label: string;
	onClick: () => void;
	portal: HTMLElement | null | undefined;
	pressed?: boolean;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button aria-label={label} aria-pressed={pressed} className={className} disabled={disabled} onClick={onClick} size="icon-sm" variant="quiet">
					{children}
				</Button>
			</TooltipTrigger>
			<TooltipContent container={portal} side="top">{label}</TooltipContent>
		</Tooltip>
	);
}

/**
 * A native range input, kept for its keyboard, pointer, and screen reader
 * behavior, laid invisibly over a drawn track. The track is inset by half the
 * input's thumb so the drawn position matches where the pointer grabs. Hover
 * and keyboard focus show the thumb through --slider-thumb rather than a
 * group-has variant, which would restyle the whole app on DOM changes.
 */
function MediaSlider({ className, disabled, label, max, onChange, secondary = 0, step, value, valueText, volume }: {
	className?: string;
	disabled?: boolean;
	label: string;
	max: number;
	onChange: (value: number) => void;
	/** A lighter fill behind the value, such as how much has loaded. */
	secondary?: number;
	step: number | "any";
	value: number;
	valueText: string;
	/** Arrow keys adjust this slider natively instead of skipping through the video. */
	volume?: boolean;
}) {
	const percent = (amount: number) => `${max > 0 ? Math.min(Math.max(amount / max, 0), 1) * 100 : 0}%`;
	return (
		<div
			className={cn(
				"relative flex h-4 items-center rounded-sm hover:[--slider-thumb:1] has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-ring has-focus-visible:[--slider-thumb:1]",
				disabled && "opacity-50",
				className,
			)}
		>
			<div className="pointer-events-none absolute inset-x-1.5 h-1 rounded-full bg-interactive-active">
				{secondary > 0 ? <div className="absolute inset-y-0 left-0 rounded-full bg-passive/40" style={{ width: percent(secondary) }} /> : null}
				<div className="absolute inset-y-0 left-0 rounded-full bg-foreground" style={{ width: percent(value) }} />
				<div
					className="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground opacity-[var(--slider-thumb,0)] transition-opacity duration-(--duration-fast) motion-reduce:transition-none"
					style={{ left: percent(value) }}
				/>
			</div>
			<input
				aria-label={label}
				aria-valuetext={valueText}
				className="absolute inset-0 m-0 w-full cursor-pointer appearance-none bg-transparent opacity-0 disabled:cursor-default [&::-webkit-slider-thumb]:size-3 [&::-webkit-slider-thumb]:appearance-none"
				data-video-volume={volume ? "" : undefined}
				disabled={disabled}
				max={max}
				min={0}
				onChange={(event) => onChange(Number(event.currentTarget.value))}
				step={step}
				type="range"
				value={value}
			/>
		</div>
	);
}
