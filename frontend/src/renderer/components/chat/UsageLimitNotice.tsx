import { useEffect, useState } from "react";
import type { TFunction } from "i18next";
import { Trans, useTranslation } from "react-i18next";
import { Gauge, Loader2, Play } from "lucide-react";
import { cn } from "../../lib/utils";
import type { ConversationUsageLimit } from "../../types/conversation";
import { Button } from "../ui/button";

export interface UsageLimitNoticeProps {
	usageLimit: ConversationUsageLimit;
	onResume?: () => void;
	onAutoResumeChange?: (enabled: boolean) => void;
	pending?: boolean;
	disabled?: boolean;
	error?: string;
	/** A historical marker; current resume controls belong by the composer. */
	compact?: boolean;
}

const resetTimeFormatter = new Intl.DateTimeFormat(undefined, {
	dateStyle: "medium",
	timeStyle: "short",
});

function remainingTime(milliseconds: number, t: TFunction): string {
	const seconds = Math.max(0, Math.ceil(milliseconds / 1000));
	const hours = Math.floor(seconds / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	if (hours > 0) return t("chat.usageLimit.remainingHours", { hours, minutes });
	if (minutes > 0) return t("chat.usageLimit.remainingMinutes", { minutes, seconds: seconds % 60 });
	return t("chat.usageLimit.remainingSeconds", { seconds });
}

/** The timer updates presentation only; the daemon owns automatic resumption. */
export function UsageLimitNotice({
	usageLimit,
	onResume,
	onAutoResumeChange,
	pending = false,
	disabled = false,
	error,
	compact = false,
}: UsageLimitNoticeProps) {
	const { t } = useTranslation();
	const [now, setNow] = useState(Date.now);
	const parsedReset = usageLimit.resetsAt ? Date.parse(usageLimit.resetsAt) : NaN;
	const resetAt = Number.isFinite(parsedReset) ? parsedReset : undefined;
	const ready = resetAt === undefined || now >= resetAt;
	const actionError = error ?? usageLimit.resumeError;
	const showAutoResume = resetAt !== undefined && onAutoResumeChange && (!ready || usageLimit.autoResume);

	useEffect(() => {
		if (compact || resetAt === undefined) return;
		const update = () => setNow(Date.now());
		update();
		const timer = window.setInterval(() => {
			update();
			if (Date.now() >= resetAt) window.clearInterval(timer);
		}, 1000);
		window.addEventListener("focus", update);
		document.addEventListener("visibilitychange", update);
		return () => {
			window.clearInterval(timer);
			window.removeEventListener("focus", update);
			document.removeEventListener("visibilitychange", update);
		};
	}, [compact, resetAt]);

	return (
		<div
			data-testid="usage-limit-notice"
			aria-busy={pending || undefined}
			className={cn(
				"flex min-w-0 items-start gap-2.5",
				compact ? "py-1" : "rounded-md border border-warning/20 bg-warning/10 px-3 py-2.5",
			)}
		>
			<Gauge aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />
			<div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1.5">
				<strong className="text-balance text-xs font-medium text-warning">{t("chat.usageLimit.title")}</strong>
				{!compact ? (
					<>
						<span className="text-pretty text-xs text-muted-foreground">
							{resetAt === undefined ? t("chat.usageLimit.unknownReset") : (
								<Trans
									t={t} i18nKey="chat.usageLimit.resetsAt"
									values={{ time: resetTimeFormatter.format(resetAt) }}
									components={{ time: <time dateTime={usageLimit.resetsAt} /> }}
								/>
							)}
						</span>
						<span className="text-pretty text-xs text-muted-foreground">
							{resetAt === undefined ? t("chat.usageLimit.manualResumeHint") : ready ? (
								<span role="status">{t("chat.usageLimit.ready")}</span>
							) : (
								<Trans
									t={t} i18nKey="chat.usageLimit.availableIn"
									values={{ duration: remainingTime(resetAt - now, t) }}
									components={{ duration: <span className="tabular-nums" /> }}
								/>
							)}
							{resetAt !== undefined && usageLimit.autoResume ? ` ${t("chat.usageLimit.scheduled")}` : null}
						</span>
						{onResume || showAutoResume ? (
							<div className="ml-auto flex flex-wrap items-center gap-2">
								{onResume ? (
									<Button
										type="button" size="sm" variant="outline" onClick={onResume}
										disabled={pending || disabled || !ready}
										title={!ready ? t("chat.usageLimit.resumeHint") : undefined}
										className="focus-visible:ring-2 focus-visible:ring-ring"
									>
										{pending ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Play aria-hidden="true" className="size-3.5" />}
										{t("chat.usageLimit.resume")}
									</Button>
								) : null}
								{showAutoResume ? (
									<Button
										type="button" size="sm" variant="ghost"
										onClick={() => onAutoResumeChange?.(!usageLimit.autoResume)}
										disabled={pending || disabled}
										className="focus-visible:ring-2 focus-visible:ring-ring"
									>
										{t(usageLimit.autoResume ? "chat.usageLimit.cancelAutoResume" : "chat.usageLimit.autoResume")}
									</Button>
								) : null}
							</div>
						) : null}
					</>
				) : null}
				{actionError ? <p role="alert" className="w-full whitespace-pre-wrap wrap-anywhere text-pretty text-xs text-destructive">{actionError}</p> : null}
			</div>
		</div>
	);
}
