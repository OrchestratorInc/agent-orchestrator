import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Gauge, Loader2, Play } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import type { ConversationUsageLimit } from "../../types/conversation";
import { Button } from "../ui/button";

export interface UsageLimitNoticeProps {
	usageLimit: ConversationUsageLimit;
	onResume?: () => void;
	onAutoResumeChange?: (enabled: boolean) => void;
	pending?: boolean;
	/** Prevents starting work; cancellation only waits for an in-flight request. */
	disabled?: boolean;
	error?: string;
	className?: string;
}

export function UsageLimitMarker() {
	const { t } = useTranslation();
	return (
		<div data-testid="usage-limit-marker" className="flex min-w-0 items-center gap-2.5 py-1">
			<Gauge aria-hidden="true" className="size-4 shrink-0 text-warning" />
			<strong className="text-balance text-xs font-medium text-foreground">{t("chat.usageLimit.title")}</strong>
		</div>
	);
}

/** The timer updates presentation only; the daemon owns automatic resumption. */
export function UsageLimitNotice({
	usageLimit,
	onResume,
	onAutoResumeChange,
	pending = false,
	disabled = false,
	error,
	className,
}: UsageLimitNoticeProps) {
	const { t } = useTranslation();
	const [now, setNow] = useState(Date.now);
	const parsedReset = usageLimit.resetsAt ? Date.parse(usageLimit.resetsAt) : NaN;
	const resetAt = Number.isFinite(parsedReset) ? parsedReset : undefined;
	const ready = resetAt === undefined || now >= resetAt;
	const actionError = error ?? usageLimit.resumeError;
	const scheduled = usageLimit.autoResume;
	const showResume = onResume && ready && !scheduled;
	const showAutoResume = onAutoResumeChange && (scheduled || (resetAt !== undefined && !ready));

	useEffect(() => {
		if (resetAt === undefined || ready) return;
		const update = () => setNow(Date.now());
		const timer = window.setTimeout(update, Math.min(resetAt - Date.now(), 2_147_483_647));
		window.addEventListener("focus", update);
		document.addEventListener("visibilitychange", update);
		return () => {
			window.clearTimeout(timer);
			window.removeEventListener("focus", update);
			document.removeEventListener("visibilitychange", update);
		};
	}, [resetAt, ready, now]);

	if (!showResume && !showAutoResume && !actionError) return null;
	return (
		<div
			data-testid="usage-limit-notice"
			aria-busy={pending || undefined}
			className={cn("flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1", className)}
		>
			{scheduled ? (
				<span role="status" className="text-pretty text-xs text-muted-foreground">
					{t(ready ? "chat.usageLimit.waiting" : "chat.usageLimit.scheduled")}
				</span>
			) : null}
			{showResume ? (
				<Button
					type="button" size="sm" variant="primary" onClick={onResume}
					disabled={pending || disabled}
				>
					{pending ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Play aria-hidden="true" className="size-3.5" />}
					{t("chat.usageLimit.resume")}
				</Button>
			) : null}
			{showAutoResume ? (
				<Button
					type="button" size="sm" variant={scheduled ? "ghost" : "secondary"}
					onClick={() => onAutoResumeChange?.(!usageLimit.autoResume)}
					disabled={pending || (!scheduled && disabled)}
				>
					{t(usageLimit.autoResume ? "chat.usageLimit.cancelAutoResume" : "chat.usageLimit.autoResume")}
				</Button>
			) : null}
			{actionError ? <p role="alert" className="basis-full whitespace-pre-wrap wrap-anywhere text-pretty text-xs text-destructive">{actionError}</p> : null}
		</div>
	);
}
