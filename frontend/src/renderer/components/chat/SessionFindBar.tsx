import { ChevronDown, ChevronUp, Loader2, X } from "lucide-react";
import { useEffect, useRef, type MouseEvent as ReactMouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";

type SessionFindBarProps = {
	query: string;
	onQueryChange: (query: string) => void;
	/** Matches in this conversation, in order; the bar steps through them. */
	total: number;
	/** Zero-based position of the current match, or -1 before one is shown. */
	index: number;
	loading: boolean;
	onNext: () => void;
	onPrevious: () => void;
	onClose: () => void;
};

/**
 * Find in the open Chat conversation.
 *
 * A search result opens its session here with the search already typed in, so
 * Enter / Shift+Enter walk the rest of the session's matches from the one that
 * was opened. Matching is the session index's (the same words, word forms and
 * tool output as the search that found the session), not the visible page's
 * text, so a match in a message that is not rendered yet is still reachable.
 * It mirrors the terminal's find bar: same placement, keys and labels.
 */
export function SessionFindBar({ query, onQueryChange, total, index, loading, onNext, onPrevious, onClose }: SessionFindBarProps) {
	const { t } = useTranslation();
	const inputRef = useRef<HTMLInputElement | null>(null);

	// Opening the bar is the point of arriving from a search: focus it so the
	// next Enter steps instead of reaching the composer.
	useEffect(() => {
		inputRef.current?.focus();
		inputRef.current?.select();
	}, []);

	const current = total > 0 && index >= 0 ? index + 1 : 0;
	const keepInputFocus = (event: ReactMouseEvent) => event.preventDefault();
	const canStep = total > 1;

	return (
		<div
			aria-label={t("chat.findLabel")}
			className="absolute right-3 top-2 z-20 flex h-9 w-[22rem] max-w-[calc(100%-1.5rem)] items-center gap-0.5 rounded-md border border-border bg-surface/95 px-1 shadow-lg backdrop-blur-sm"
			data-testid="session-find-bar"
			onKeyDown={(event) => {
				event.stopPropagation();
				if (event.key === "Escape") {
					event.preventDefault();
					onClose();
				} else if (event.key === "Enter") {
					event.preventDefault();
					if (event.shiftKey) onPrevious();
					else onNext();
				}
			}}
			role="search"
		>
			<input
				aria-label={t("chat.findLabel")}
				className="h-7 min-w-0 flex-1 bg-transparent px-2 text-xs text-foreground outline-none placeholder:text-muted-foreground"
				maxLength={512}
				onChange={(event) => onQueryChange(event.target.value)}
				placeholder={t("chat.findPlaceholder")}
				ref={inputRef}
				type="search"
				value={query}
			/>
			<span
				aria-label={t("terminal.searchResults", { current, total })}
				aria-live="polite"
				className="flex min-w-9 items-center justify-center text-2xs tabular-nums text-muted-foreground"
				data-testid="session-find-count"
			>
				{loading ? <Loader2 aria-hidden="true" className="size-3 animate-spin motion-reduce:animate-none" /> : `${current}/${total}`}
			</span>
			<div className="mx-0.5 h-4 w-px bg-border" />
			<Button
				aria-label={t("terminal.searchPrevious")}
				className="size-7 text-muted-foreground"
				disabled={!canStep}
				onClick={onPrevious}
				onMouseDown={keepInputFocus}
				size="icon-sm"
				type="button"
				variant="ghost"
			>
				<ChevronUp className="size-icon-sm" aria-hidden="true" />
			</Button>
			<Button
				aria-label={t("terminal.searchNext")}
				className="size-7 text-muted-foreground"
				disabled={!canStep}
				onClick={onNext}
				onMouseDown={keepInputFocus}
				size="icon-sm"
				type="button"
				variant="ghost"
			>
				<ChevronDown className="size-icon-sm" aria-hidden="true" />
			</Button>
			<Button
				aria-label={t("files.closeSearch")}
				className="size-7 text-muted-foreground"
				onClick={onClose}
				size="icon-sm"
				type="button"
				variant="ghost"
			>
				<X className="size-icon-sm" aria-hidden="true" />
			</Button>
		</div>
	);
}
