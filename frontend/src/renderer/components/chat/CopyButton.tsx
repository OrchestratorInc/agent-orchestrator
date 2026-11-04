/**
 * Copy to clipboard, with the only feedback that matters: whether it worked.
 *
 * A denied clipboard says nothing rather than claiming "Copied" — a false
 * confirmation costs the user the text they thought they had. Shared by the code
 * block and the message action row so both confirm the same way.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { aoBridge } from "../../lib/bridge";
import { Button, type ButtonProps } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/** Long enough to be noticed, short enough that a second copy reads as a second copy. */
const CONFIRM_MS = 1400;

export function CopyButton({
	text,
	label,
	/** Icon-only, for places where a word of chrome would be one too many. */
	compact,
	/** Overrides the default size (28px icon when compact, 28px text otherwise). */
	size,
	className,
	/** Confirmation once the text is on the clipboard; callers outside Chat pass a localized one. */
	copiedLabel = "Copied",
}: {
	text: string;
	label: string;
	compact?: boolean;
	size?: ButtonProps["size"];
	className?: string;
	copiedLabel?: string;
}) {
	const [copied, setCopied] = useState(false);
	const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

	useEffect(() => () => clearTimeout(timer.current), []);

	const copy = useCallback(() => {
		// Through the bridge rather than `navigator.clipboard`: in Electron that
		// reaches the native clipboard, which does not need the document to be focused
		// or a permission the renderer cannot prompt for.
		void aoBridge.clipboard.writeText(text).then(
			() => {
				setCopied(true);
				clearTimeout(timer.current);
				timer.current = setTimeout(() => setCopied(false), CONFIRM_MS);
			},
			() => {
				// Clipboard denied. Nothing to say that would not be a guess.
			},
		);
	}, [text]);

	const button = (
		<Button
			type="button"
			variant="quiet"
			size={size ?? (compact ? "icon-sm" : "sm")}
			onClick={copy}
			aria-label={copied ? copiedLabel : label}
			className={className}
		>
			{copied ? (
				<Check aria-hidden="true" className="size-3 text-success" />
			) : (
				<Copy aria-hidden="true" className="size-3" />
			)}
			{compact ? null : copied ? copiedLabel : "Copy"}
		</Button>
	);

	// Icon-only leaves nothing on screen to explain itself, so the tooltip
	// carries the label there and only there.
	if (!compact) return button;

	return (
		<Tooltip>
			<TooltipTrigger asChild>{button}</TooltipTrigger>
			<TooltipContent side="bottom">{copied ? copiedLabel : label}</TooltipContent>
		</Tooltip>
	);
}
