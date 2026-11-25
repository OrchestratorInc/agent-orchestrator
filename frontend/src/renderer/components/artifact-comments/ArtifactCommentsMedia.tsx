import { useEffect, useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import {
	artifactCommentAnchorKey,
	artifactCommentAnchorKind,
	artifactCommentContainedBox,
	artifactCommentRegionFromDrag,
	formatArtifactCommentTime,
	groupArtifactCommentThreads,
	imageCommentAnchor,
	isPointRegion,
	videoCommentAnchor,
	type ArtifactCommentAnchor,
	type ArtifactCommentBox,
	type ArtifactCommentGroup,
	type ArtifactCommentPoint,
	type ArtifactCommentRegion,
	type ArtifactCommentThread,
	type CommentsSelectionRect,
} from "../../../shared/artifact-comments";
import { artifactCommentAnchorText } from "./anchor-label";

/** How close (seconds) the video must be to a comment's time for its region to show on the frame. */
const REGION_SHOWS_WITHIN = 0.5;

const percent = (value: number) => `${value * 100}%`;
const regionStyle = (region: ArtifactCommentRegion) => ({ left: percent(region.x), top: percent(region.y), width: percent(region.w), height: percent(region.h) });
const pinStyle = (region: ArtifactCommentRegion) => (isPointRegion(region) ? { left: percent(region.x), top: percent(region.y) } : regionStyle(region));

const PIN = "absolute flex h-5 min-w-5 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border-2 border-background bg-logo-accent px-1 text-[11px] leading-none font-semibold text-logo-accent-foreground tabular-nums shadow-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground";

/**
 * A published image or video in comment mode (the app's comment workspace
 * is comment mode). The app draws the file itself, so an overlay on the
 * drawn picture picks: a click is a point, a drag a rectangle, as fractions
 * of the picture. Open groups show as numbered pins or outlined rectangles; a
 * click opens the group's popover. A video plays under the timeline's own
 * controls (the overlay takes clicks on the frame): a dot per open group,
 * "Comment at 01:23" for a moment without a place; picking pauses it, and
 * selecting a group seeks to its time and pauses.
 */
export function ArtifactCommentsMedia({ kind, onPick, onSelect, onSelection, onShown, picked, selected, src, threads, title }: {
	kind: "image" | "video";
	src: string;
	title: string;
	threads: readonly ArtifactCommentThread[];
	/** The anchor a new comment is being written on. */
	picked: ArtifactCommentAnchor | null;
	/** The open popover's group key. */
	selected: string | null;
	onPick: (anchor: ArtifactCommentAnchor) => void;
	onSelect: (key: string) => void;
	/** The selected pin, rectangle or dot's top and bottom inside this view, for the popover; null when none. */
	onSelection: (rect: CommentsSelectionRect | null) => void;
	onShown?: () => void;
}) {
	const { t } = useTranslation();
	const rootRef = useRef<HTMLDivElement>(null);
	const imageRef = useRef<HTMLImageElement>(null);
	const videoRef = useRef<HTMLVideoElement>(null);
	const overlayRef = useRef<HTMLDivElement>(null);
	const [box, setBox] = useState<ArtifactCommentBox | null>(null);
	const [drag, setDrag] = useState<{ from: ArtifactCommentPoint; to: ArtifactCommentPoint } | null>(null);
	const [time, setTime] = useState(0);
	const [duration, setDuration] = useState(0);
	const [playing, setPlaying] = useState(false);

	useLayoutEffect(() => {
		const element = kind === "image" ? imageRef.current : videoRef.current;
		if (!element) return;
		const measure = () => {
			const natural = element instanceof HTMLImageElement
				? { width: element.naturalWidth, height: element.naturalHeight }
				: { width: element.videoWidth, height: element.videoHeight };
			if (!(natural.width > 0 && natural.height > 0)) return setBox(null);
			setBox(artifactCommentContainedBox(natural, { width: element.clientWidth, height: element.clientHeight }));
		};
		measure();
		const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
		observer?.observe(element);
		const event = kind === "image" ? "load" : "loadedmetadata";
		element.addEventListener(event, measure);
		return () => {
			observer?.disconnect();
			element.removeEventListener(event, measure);
		};
	}, [kind, src]);

	const groups = useMemo(
		() => groupArtifactCommentThreads(threads).open.filter((group) => artifactCommentAnchorKind(group.anchor) === kind),
		[threads, kind],
	);

	const selectedTime = useMemo(() => {
		if (kind !== "video" || selected === null) return null;
		if (picked && artifactCommentAnchorKey(picked) === selected) return picked.time ?? null;
		return groups.find((group) => group.path === selected)?.anchor.time ?? null;
	}, [kind, selected, picked, groups]);
	useEffect(() => {
		const video = videoRef.current;
		if (!video || selectedTime === null) return;
		video.pause();
		if (Math.abs(video.currentTime - selectedTime) > 0.01) video.currentTime = selectedTime;
	}, [selectedTime]);

	// Where the selected pin, rectangle or dot is, for the popover beside it.
	// Measured after every render (the box, the time and the selection all move
	// it), and reported only when it changes.
	const onSelectionRef = useRef(onSelection);
	onSelectionRef.current = onSelection;
	const lastSelection = useRef<string | undefined>(undefined);
	useLayoutEffect(() => {
		const root = rootRef.current;
		const target = root?.querySelector<HTMLElement>("[data-selection-target]");
		let next: CommentsSelectionRect | null = null;
		if (root && target) {
			const origin = root.getBoundingClientRect();
			const rect = target.getBoundingClientRect();
			next = { top: Math.round(rect.top - origin.top), bottom: Math.round(rect.bottom - origin.top) };
		}
		const key = JSON.stringify(next);
		if (key === lastSelection.current) return;
		lastSelection.current = key;
		onSelectionRef.current(next);
	});

	const pointAt = (event: ReactPointerEvent<HTMLDivElement>): ArtifactCommentPoint => {
		const rect = overlayRef.current!.getBoundingClientRect();
		const clamp = (value: number) => Math.min(1, Math.max(0, value));
		return {
			x: rect.width > 0 ? clamp((event.clientX - rect.left) / rect.width) : 0,
			y: rect.height > 0 ? clamp((event.clientY - rect.top) / rect.height) : 0,
		};
	};
	const pickAt = (region: ArtifactCommentRegion) => onPick(kind === "image" ? imageCommentAnchor(region) : videoCommentAnchor(videoRef.current?.currentTime ?? 0, region));

	const visible = groups.filter((group) => {
		if (!group.anchor.region) return false;
		if (kind === "image") return true;
		return group.path === selected || Math.abs((group.anchor.time ?? 0) - time) <= REGION_SHOWS_WITHIN;
	});
	const pickedHere = picked && artifactCommentAnchorKind(picked) === kind ? picked : null;
	const draftRegion = drag ? artifactCommentRegionFromDrag(drag.from, drag.to) : pickedHere?.region;
	const markerLabel = (group: ArtifactCommentGroup) => t("artifactComments.markerLabel", { label: artifactCommentAnchorText(t, group.anchor) ?? "", n: group.openCount });

	const overlay = box ? (
		<div
			className="absolute cursor-crosshair touch-none"
			data-testid="artifact-comments-media-overlay"
			onPointerCancel={() => setDrag(null)}
			onPointerDown={(event) => {
				if (event.button !== 0) return;
				event.preventDefault();
				event.currentTarget.setPointerCapture?.(event.pointerId);
				videoRef.current?.pause();
				const point = pointAt(event);
				setDrag({ from: point, to: point });
			}}
			onPointerMove={(event) => {
				if (drag) setDrag({ from: drag.from, to: pointAt(event) });
			}}
			onPointerUp={(event) => {
				if (!drag) return;
				const region = artifactCommentRegionFromDrag(drag.from, pointAt(event));
				setDrag(null);
				pickAt(region);
			}}
			ref={overlayRef}
			style={{ left: box.left, top: box.top, width: box.width, height: box.height }}
		>
			{visible.map((group) => {
				const region = group.anchor.region!;
				const isSelected = group.path === selected && !pickedHere;
				const count = group.openCount > 99 ? "99+" : String(group.openCount);
				const stop = (event: ReactPointerEvent) => event.stopPropagation();
				const pin = (
					<button
						aria-label={markerLabel(group)}
						className={cn(PIN, !isPointRegion(region) && "top-0 left-full", isSelected && "ring-2 ring-logo-accent ring-offset-1")}
						data-selection-target={isSelected && isPointRegion(region) ? "" : undefined}
						key={group.path}
						onClick={() => onSelect(group.path)}
						onPointerDown={stop}
						onPointerUp={stop}
						style={isPointRegion(region) ? pinStyle(region) : undefined}
						type="button"
					>
						{count}
					</button>
				);
				if (isPointRegion(region)) return pin;
				return (
					<span
						className={cn("pointer-events-none absolute rounded-sm border-[1.5px] border-logo-accent", isSelected && "border-2 bg-logo-accent/15")}
						data-selection-target={isSelected ? "" : undefined}
						key={group.path}
						style={regionStyle(region)}
					>
						<span className="pointer-events-auto">{pin}</span>
					</span>
				);
			})}
			{draftRegion ? (
				<span
					aria-hidden="true"
					className={cn(
						"pointer-events-none absolute border-2 border-dashed border-logo-accent bg-logo-accent/15",
						isPointRegion(draftRegion) ? "size-3.5 -translate-x-1/2 -translate-y-1/2 rounded-full" : "rounded-sm",
					)}
					data-selection-target={drag ? undefined : ""}
					data-testid="artifact-comments-media-draft"
					style={pinStyle(draftRegion)}
				/>
			) : null}
		</div>
	) : null;

	if (kind === "image") {
		return (
			<div className="relative h-full w-full bg-background" ref={rootRef}>
				<img
					alt={title}
					className="absolute inset-0 h-full w-full object-contain select-none"
					draggable={false}
					onLoad={() => onShown?.()}
					ref={imageRef}
					referrerPolicy="no-referrer"
					src={src}
				/>
				{overlay}
			</div>
		);
	}

	const seekTo = (fraction: number) => {
		const video = videoRef.current;
		if (video && duration > 0) video.currentTime = Math.min(duration, Math.max(0, fraction * duration));
	};
	return (
		<div className="flex h-full w-full flex-col bg-background" ref={rootRef}>
			<div className="relative min-h-0 flex-1">
				<video
					aria-label={title}
					className="absolute inset-0 h-full w-full object-contain"
					onDurationChange={(event) => setDuration(Number.isFinite(event.currentTarget.duration) ? event.currentTarget.duration : 0)}
					onLoadedMetadata={(event) => {
						setDuration(Number.isFinite(event.currentTarget.duration) ? event.currentTarget.duration : 0);
						onShown?.();
					}}
					onPause={() => setPlaying(false)}
					onPlay={() => setPlaying(true)}
					onSeeked={(event) => setTime(event.currentTarget.currentTime)}
					onTimeUpdate={(event) => setTime(event.currentTarget.currentTime)}
					playsInline
					preload="metadata"
					ref={videoRef}
					src={src}
				/>
				{overlay}
			</div>
			<div className="flex h-10 shrink-0 items-center gap-2 border-t border-border px-3 text-caption text-muted-foreground" data-testid="artifact-comments-timeline">
				<button
					aria-label={playing ? t("artifactComments.pause") : t("artifactComments.play")}
					className="grid size-7 place-items-center rounded-md text-foreground hover:bg-muted"
					onClick={() => {
						const video = videoRef.current;
						if (!video) return;
						if (video.paused) void video.play()?.catch(() => undefined);
						else video.pause();
					}}
					type="button"
				>
					<svg aria-hidden="true" className="size-4" fill="currentColor" viewBox="0 0 24 24">
						<path d={playing ? "M6 4h4v16H6zM14 4h4v16h-4z" : "M7 4.5v15l13-7.5z"} />
					</svg>
				</button>
				<span className="shrink-0 tabular-nums">{formatArtifactCommentTime(time)} / {formatArtifactCommentTime(duration)}</span>
				<div
					aria-label={t("artifactComments.seek")}
					aria-valuemax={Math.round(duration)}
					aria-valuemin={0}
					aria-valuenow={Math.round(time)}
					aria-valuetext={formatArtifactCommentTime(time)}
					className="relative h-1.5 min-w-0 flex-1 cursor-pointer rounded-full bg-muted focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-foreground"
					onKeyDown={(event) => {
						if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
						event.preventDefault();
						const video = videoRef.current;
						if (video) video.currentTime = Math.max(0, video.currentTime + (event.key === "ArrowLeft" ? -5 : 5));
					}}
					onPointerDown={(event) => {
						if ((event.target as HTMLElement).closest("button")) return;
						const rect = event.currentTarget.getBoundingClientRect();
						if (rect.width > 0) seekTo((event.clientX - rect.left) / rect.width);
					}}
					role="slider"
					tabIndex={0}
				>
					<span className="pointer-events-none absolute inset-y-0 left-0 rounded-full bg-muted-foreground/50" style={{ width: duration > 0 ? percent(Math.min(1, time / duration)) : 0 }} />
					{duration > 0
						? groups.map((group) => {
							const isSelected = group.path === selected && !pickedHere;
							return (
								<button
									aria-label={markerLabel(group)}
									className={cn(PIN, "top-1/2 h-3.5 min-w-3.5 text-[9px]", isSelected && "ring-2 ring-logo-accent ring-offset-1")}
									data-selection-target={isSelected && !group.anchor.region ? "" : undefined}
									key={group.path}
									onClick={() => onSelect(group.path)}
									style={{ left: percent(Math.min(1, (group.anchor.time ?? 0) / duration)) }}
									type="button"
								>
									{group.openCount > 1 ? group.openCount : null}
								</button>
							);
						})
						: null}
				</div>
				<button
					className="shrink-0 rounded-md border border-border px-2 py-1 text-foreground hover:bg-muted"
					onClick={() => {
						const video = videoRef.current;
						video?.pause();
						onPick(videoCommentAnchor(video?.currentTime ?? 0));
					}}
					type="button"
				>
					{t("artifactComments.commentAt", { time: formatArtifactCommentTime(time) })}
				</button>
			</div>
		</div>
	);
}
