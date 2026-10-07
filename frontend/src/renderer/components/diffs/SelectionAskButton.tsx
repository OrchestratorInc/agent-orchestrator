import { useLayoutEffect, useRef, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { MessageSquarePlus } from "lucide-react";
import { isMacPlatform } from "../../lib/platform";
import { Button } from "../ui/button";
import type { CodeSelectionSource } from "./useCodeSelection";

// "Ask in chat" for highlighted code, like an editor's "add to chat": it sits
// just past the end of the selection and follows it while it is dragged out,
// then becomes clickable once the mouse is released. Pressing it keeps the
// selection, so it asks about the range on screen.
export function SelectionAskButton({ onAsk, source }: { onAsk: () => void; source: CodeSelectionSource }) {
	const { t } = useTranslation();
	const selection = useSyncExternalStore(source.subscribe, source.get, () => null);
	const buttonRef = useRef<HTMLDivElement>(null);
	// Past the end of the selected text; without room there, under that line.
	useLayoutEffect(() => {
		const button = buttonRef.current;
		if (!button || !selection) return;
		const width = button.offsetWidth;
		const height = button.offsetHeight;
		const fits = selection.point.x + 6 + width <= window.innerWidth - 8;
		button.style.left = `${fits ? selection.point.x + 6 : Math.max(8, window.innerWidth - width - 8)}px`;
		button.style.top = `${fits ? selection.point.y - height / 2 : selection.point.y + 12}px`;
	}, [selection]);
	if (!selection) return null;
	const label = t("files.askInChat");
	return createPortal(
		<div
			className="z-overlay flex items-center rounded-lg border border-border bg-card p-0.5 font-sans shadow-md"
			data-selection-ask=""
			onMouseDown={(event) => event.preventDefault()}
			ref={buttonRef}
			// Mid-drag it sits under the pointer: let the drag pass through it.
			style={{
				left: selection.point.x + 6,
				opacity: selection.dragging ? 0.85 : 1,
				pointerEvents: selection.dragging ? "none" : undefined,
				position: "fixed",
				top: selection.point.y - 14,
			}}
		>
			<Button
				className="h-6 gap-1.5 rounded-md px-2 text-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground [&_svg]:size-3.5"
				onClick={onAsk}
				size={null}
				title={`${label} (${isMacPlatform() ? "⌘L" : "Ctrl+L"})`}
				type="button"
				variant="ghost"
			>
				<MessageSquarePlus aria-hidden="true" />
				{label}
			</Button>
		</div>,
		document.body,
	);
}
