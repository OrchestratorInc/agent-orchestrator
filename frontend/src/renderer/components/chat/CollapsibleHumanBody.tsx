import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { useReducedMotion } from "motion/react";
import { ChevronDown } from "@/components/app-icons";
import { Button } from "../ui/button";
import { CHAT_REVEAL_RANGE_EVENT, type ChatRevealRangeDetail } from "../../lib/chat-annotation-navigation";
import { cn } from "../../lib/utils";

const FOLD_LINES = 10;
const MIN_HIDDEN_LINES = 2;
const ANIMATION_FALLBACK_MS = 300;
const FADE_PX = 40;

const foldHeights = new Map<string, number>();
const openedMessages = new Set<string>();
const REMEMBERED_MESSAGES = 200;

function remember<T>(store: Map<string, T> | Set<string>, key: string, value?: T) {
	store.delete(key);
	if (store instanceof Set) store.add(key);
	else store.set(key, value as T);
	if (store.size > REMEMBERED_MESSAGES) store.delete(store.keys().next().value as string);
}

type Animation = { height: number; collapsing: boolean };

export function CollapsibleHumanBody({ stateKey, scrollWhenFolded = false, children }: {
	stateKey: string;
	scrollWhenFolded?: boolean;
	children: ReactNode;
}) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const clipId = useId();
	const clip = useRef<HTMLDivElement>(null);
	const content = useRef<HTMLDivElement>(null);
	const [foldHeight, setFoldHeight] = useState(() => foldHeights.get(stateKey));
	const [open, setOpen] = useState(() => openedMessages.has(stateKey));
	const [animation, setAnimation] = useState<Animation>();
	const fallbackTimer = useRef<number | undefined>(undefined);

	useLayoutEffect(() => {
		const clipNode = clip.current;
		const contentNode = content.current;
		if (!clipNode || !contentNode) return;
		const measure = () => {
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
			setAnimation({ height: contentNode.offsetHeight, collapsing: false });
			setOpen(true);
			return;
		}
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

	useEffect(() => {
		const clipNode = clip.current;
		if (!folded || !clipNode) return;
		const reveal = (event: Event) => {
			const { range } = (event as CustomEvent<ChatRevealRangeDetail>).detail;
			if (range.getBoundingClientRect().bottom <= clipNode.getBoundingClientRect().bottom - FADE_PX) return;
			event.preventDefault();
			remember(openedMessages, stateKey);
			finishAnimation();
			setOpen(true);
		};
		clipNode.addEventListener(CHAT_REVEAL_RANGE_EVENT, reveal);
		return () => clipNode.removeEventListener(CHAT_REVEAL_RANGE_EVENT, reveal);
	}, [folded, stateKey, finishAnimation]);

	return (
		<>
			<div
				ref={clip}
				id={clipId}
				className={cn(folds && "chat-human-fold", folds && scrollWhenFolded && "chat-composer-scrollbar")}
				data-folded={folded ? "" : undefined}
				data-scroll-folded={scrollWhenFolded ? "" : undefined}
				data-animating={animation ? "" : undefined}
				data-collapsing={animation?.collapsing ? "" : undefined}
				style={height === undefined ? undefined : { height }}
				onTransitionEnd={(event) => {
					if (event.target === event.currentTarget && event.propertyName === "height") finishAnimation();
				}}
			>
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
