import { Coffee, Info } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { awakeCapability, useAwake } from "../hooks/useAwake";
import { useUiStore } from "../stores/ui-store";
import { isMacPlatform } from "../lib/platform";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

export function AwakeInfo() {
	const { t } = useTranslation();
	const open = useUiStore((s) => s.openGlobalSettings);
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					type="button"
					aria-label={t("awake.openSettings", "Stay Awake settings")}
					onClick={() => open("stayAwake")}
					className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm text-settings-muted hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					<Info className="size-icon-sm" aria-hidden="true" />
				</button>
			</TooltipTrigger>
			<TooltipContent className="max-w-64">
				<p>{t("awake.title", "Stay Awake")}</p>
				<p>
					{t(
						"awake.tooltip",
						"Keeps this Mac awake indefinitely. With supported lid control enabled, you can also close the lid. Battery and heat protection still apply.",
					)}
				</p>
			</TooltipContent>
		</Tooltip>
	);
}

export function AwakeControl(props: { sessionId?: string; compact?: boolean }) {
	if (!isMacPlatform()) return null;
	return <MacAwakeControl {...props} />;
}
function MacAwakeControl({
	sessionId,
	compact = false,
}: {
	sessionId?: string;
	compact?: boolean;
}) {
	const { t } = useTranslation();
	const awake = useAwake(isMacPlatform());
	const [scope, setScope] = useState<"session" | "ao">(
		sessionId ? "session" : "ao",
	);
	if (!isMacPlatform()) return null;
	const active =
		awake.state?.sessions?.filter(
			(s) =>
				!s.endedAt &&
				s.scope === scope &&
				(scope === "ao" || s.sessionId === sessionId),
		) ?? [];
	const checked = active.length > 0;
	const waiting = checked && !awake.state?.active;
	const pendingReason = checked
		? awake.state?.reason ||
			(waiting
				? "starting"
				: active.some((source) => source.lidClosed) && !awake.state?.lidActive
					? "starting"
					: "")
		: "";
	const status = pendingReason
		? t(
				`mobile.keepAwake.lidReason.${pendingReason}`,
				pendingReason.replaceAll("_", " "),
			)
		: null;
	const supported = awakeCapability(awake.state, "idle")?.available ?? false;
	const toggle = (next: boolean) => {
		if (next)
			awake.act({
				kind: "start",
				request: {
					scope,
					sessionId: scope === "session" ? sessionId : "",
					mode: "indefinite",
					lidClosed: awake.state?.settings.lidClosed ?? true,
				},
			});
		else for (const source of active) awake.act({ kind: "end", id: source.id });
	};
	const error = awake.error instanceof Error ? awake.error.message : null;
	return (
		<div
			className={compact ? "inline-flex items-center gap-0.5" : "py-1"}
			data-testid={compact ? "awake-toolbar" : "awake-session-control"}
			style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
		>
			<div className="flex min-h-8 items-center justify-between gap-2">
				{compact ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								type="button"
								variant="ghost"
								size="icon-sm"
								aria-label={t("awake.title", "Stay Awake")}
								aria-pressed={checked}
								aria-busy={waiting || undefined}
								className={error ? "text-error" : checked ? "text-accent" : "text-passive"}
								disabled={!supported || awake.busy}
								onClick={() => toggle(!checked)}
							>
								<Coffee className="size-icon-sm" aria-hidden="true" />
							</Button>
						</TooltipTrigger>
						<TooltipContent>
							{t("awake.title", "Stay Awake")}
							{error ? <p>{error}</p> : status ? <p>{status}</p> : null}
						</TooltipContent>
					</Tooltip>
				) : (
					<>
						<div className="flex min-w-0 items-center gap-1">
							<span className="text-sm text-settings-label">
								{t("awake.title", "Stay Awake")}
							</span>
							<AwakeInfo />
						</div>
						<div className="flex shrink-0 items-center gap-2">
							{sessionId ? (
								<SettingsOptionMenu
									aria-label={t("awake.scope", "Stay Awake scope")}
									value={scope}
									options={[
										{
											value: "session",
											label: t("awake.forSession", "For this session"),
										},
										{ value: "ao", label: t("awake.forAO", "For AO") },
									]}
									onChange={setScope}
									disabled={awake.busy}
								/>
							) : null}
							<Switch
								aria-label={t("awake.title", "Stay Awake")}
								checked={checked}
								disabled={!supported || awake.busy}
								onCheckedChange={toggle}
							/>
						</div>
					</>
				)}
			</div>
			{compact ? <AwakeInfo /> : null}
			{status && !compact ? (
				<p className="text-xs text-settings-muted" role="status">
					{status}
				</p>
			) : null}
			{error ? (
				<span className={compact ? "sr-only" : "text-xs break-words text-error"} role="alert">
					{error}
				</span>
			) : null}
		</div>
	);
}
