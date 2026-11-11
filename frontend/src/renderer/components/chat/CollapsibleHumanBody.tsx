import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { useReducedMotion } from "motion/react";
import { ChevronDown } from "@/components/app-icons";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

/** A long message folds to this many lines of its own text. */
const FOLD_LINES = 10;
/** It only folds when that hides at least this many more lines, so opening it never reveals a single line. */
const MIN_HIDDEN_LINES = 2;
/** Matches the height transition in styles.css; ends the animation if `transitionend` never arrives. */
const ANIMATION_FALLBACK_MS = 300;

/**
 * What each message looked like when it last rendered, keyed by its text. A bubble
 * remounts more often than it seems: the first message moves from the new chat page
 * to the session, its local echo gives way to the stored copy under a new id, and a
 * long chat virtualizes turns that scroll away. Starting from the remembered fold
 * keeps every remount from painting at full length or closing what the reader opened.
 */
const foldHeights = new Map<string, number>();
const openedMessages = new Set<string>();
/** Bounds the memory above; the oldest entries go first. */
const REMEMBERED_MESSAGES = 200;

function remember<T>(store: Map<string, T> | Set<string>, key: string, value?: T) {
	store.delete(key);
	if (store instanceof Set) store.add(key);
	else store.set(key, value as T);
	if (store.size > REMEMBERED_MESSAGES) store.delete(store.keys().next().value as string);
}

type Animation = { height: number; collapsing: boolean };

/**
 * The user's bubble body, folded when it is taller than about ten lines. The fold
 * is measured from the rendered content, so pasted text and code count by the room
 * they take. Attachments stay outside it: a normal screenshot is taller than the
 * fold, so inside it would always be cut off. Short messages render exactly as before.
 */
export function CollapsibleHumanBody({ stateKey, scrollWhenFolded = false, children }: {
	/** Stays the same across the echo-to-stored handoff; the message's visible text. */
	stateKey: string;
	/** The message editor keeps the fold's height but scrolls, so every line stays reachable. */
	scrollWhenFolded?: boolean;
	children: ReactNode;
}) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const clipId = useId();
	const clip = useRef<HTMLDivElement>(null);
	const content = useRef<HTMLDivElement>(null);
	// The folded height in px, or undefined while the whole message fits.
	const [foldHeight, setFoldHeight] = useState(() => foldHeights.get(stateKey));
	const [open, setOpen] = useState(() => openedMessages.has(stateKey));
	// Set only while a reader's toggle is animating; measurement never animates.
	const [animation, setAnimation] = useState<Animation>();
	const fallbackTimer = useRef<number | undefined>(undefined);

	// Measured before paint, so a long message never flashes at full length first.
	useLayoutEffect(() => {
		const clipNode = clip.current;
		const contentNode = content.current;
		if (!clipNode || !contentNode) return;
		const measure = () => {
			// Not laid out (a hidden surface): keep the remembered fold rather than guess.
			if (contentNode.offsetHeight === 0) return;
			const lineHeight = Number.parseFloat(getComputedStyle(clipNode).lineHeight) || 22;
			const fold = Math.round(lineHeight * FOLD_LINES);
			const next = contentNode.offsetHeight > fold + lineHeight * MIN_HIDDEN_LINES ? fold : undefined;
			if (next === undefined) foldHeights.delete(stateKey);
			else remember(foldHeights, stateKey, next);
			setFoldHeight((current) => (current === next ? current : next));
		};
		measure();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measure);
		observer.observe(contentNode);
		return () => observer.disconnect();
	}, [stateKey]);

	const finishAnimation = useCallback(() => {
		window.clearTimeout(fallbackTimer.current);
		fallbackTimer.current = undefined;
		setAnimation(undefined);
	}, []);
	useEffect(() => () => window.clearTimeout(fallbackTimer.current), []);

	const toggle = () => {
		const clipNode = clip.current;
		const contentNode = content.current;
		const next = !open;
		if (next) remember(openedMessages, stateKey);
		else openedMessages.delete(stateKey);
		if (reducedMotion || !clipNode || !contentNode || foldHeight === undefined) {
			finishAnimation();
			setOpen(next);
			return;
		}
		window.clearTimeout(fallbackTimer.current);
		fallbackTimer.current = window.setTimeout(finishAnimation, ANIMATION_FALLBACK_MS);
		if (next) {
			// From the folded height (or wherever a collapse was) to the full content.
			setAnimation({ height: contentNode.offsetHeight, collapsing: false });
			setOpen(true);
			return;
		}
		// An open body has height auto, which does not transition: pin its current
		// height first, let layout see it, then head for the fold.
		flushSync(() => {
			setAnimation({ height: clipNode.offsetHeight, collapsing: true });
			setOpen(false);
		});
		clipNode.getBoundingClientRect();
		setAnimation({ height: foldHeight, collapsing: true });
	};

	const folds = foldHeight !== undefined;
	const folded = folds && !open;
	const height = animation ? animation.height : folded ? foldHeight : undefined;

	return (
		<>
			<div
				ref={clip}
				id={clipId}
				className={cn(folds && "chat-human-fold", folds && scrollWhenFolded && "chat-composer-scrollbar")}
				data-folded={folded ? "" : undefined}
				data-scroll-folded={scrollWhenFolded ? "" : undefined}
				data-animating={animation ? "" : undefined}
				// While it shrinks, the browser anchors the view on what follows (the
				// toggle) instead of the text inside, so "Show less" stays under the pointer.
				data-collapsing={animation?.collapsing ? "" : undefined}
				style={height === undefined ? undefined : { height }}
				onTransitionEnd={(event) => {
					if (event.target === event.currentTarget && event.propertyName === "height") finishAnimation();
				}}
			>
				{/* flow-root keeps children's margins inside, so the measured height is the real one. */}
				<div ref={content} className="flow-root">
					{children}
				</div>
			</div>
			{folds ? (
				<Button
					type="button"
					variant="quiet"
					size="xs"
					aria-expanded={open}
					aria-controls={clipId}
					onClick={toggle}
					className="-mx-2 -mb-1 mt-1"
				>
					{t(open ? "shell.showLess" : "shell.showMore")}
					<ChevronDown
						aria-hidden="true"
						className={cn("size-3.5 transition-transform duration-150 ease-out motion-reduce:transition-none", open && "rotate-180")}
					/>
				</Button>
			) : null}
		</>
	);
}
