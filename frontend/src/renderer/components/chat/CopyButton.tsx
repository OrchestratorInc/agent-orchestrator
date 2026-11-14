import { cn } from "../../lib/utils";
import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy } from "@/components/app-icons";
import { aoBridge } from "../../lib/bridge";
import { Button, type ButtonProps } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

const CONFIRM_MS = 1400;

export function CopyButton({
	text,
	label,
	compact,
	size,
	className,
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
		void aoBridge.clipboard.writeText(text).then(
			() => {
				setCopied(true);
				clearTimeout(timer.current);
				timer.current = setTimeout(() => setCopied(false), CONFIRM_MS);
			},
			() => {
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
				<Check aria-hidden="true" className={cn(compact ? "size-3.5" : "size-3", "text-success")} />
			) : (
				<Copy aria-hidden="true" className={compact ? "size-3.5" : "size-3"} />
			)}
			{compact ? null : copied ? copiedLabel : "Copy"}
		</Button>
	);

	if (!compact) return button;

	return (
		<Tooltip>
			<TooltipTrigger asChild>{button}</TooltipTrigger>
			<TooltipContent side="bottom">{copied ? copiedLabel : label}</TooltipContent>
		</Tooltip>
	);
}
