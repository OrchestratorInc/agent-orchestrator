import { Check, CircleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AgentStatusTone, RankedAgentOption } from "../../lib/agent-select-options";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";

const STATUS_TONE_CLASS: Record<AgentStatusTone, string> = {
	success: "text-success",
	warning: "text-warning",
	muted: "text-settings-muted",
};

export function AgentSelectMenuItem({
	agentId,
	label,
	selected,
	status,
	statusTone,
	statusIndicator,
	disabled = false,
}: {
	agentId?: string;
	label: string;
	selected: boolean;
	status?: string;
	statusTone?: AgentStatusTone;
	statusIndicator?: RankedAgentOption["statusIndicator"];
	disabled?: boolean;
}) {
	const { t } = useTranslation();
	const rowRef = useRef<HTMLSpanElement>(null);
	const [tooltipOpen, setTooltipOpen] = useState(false);
	useEffect(() => {
		if (statusIndicator !== "auth-unknown") return;
		const item = rowRef.current?.closest('[role="menuitem"], [role="option"]');
		if (!item) return;
		const show = () => setTooltipOpen(true);
		const hide = () => setTooltipOpen(false);
		item.addEventListener("focus", show);
		item.addEventListener("blur", hide);
		if (item === document.activeElement) show();
		return () => {
			item.removeEventListener("focus", show);
			item.removeEventListener("blur", hide);
		};
	}, [statusIndicator]);

	return (
		<span ref={rowRef} className={cn("flex min-w-0 w-full items-center gap-3", disabled && "opacity-45")}>
			{agentId ? (
				<AgentAvatar provider={agentId} className="size-icon-lg" decorative />
			) : (
				<span className="size-icon-lg shrink-0" aria-hidden="true" />
			)}
			<span className={`min-w-0 flex-1 truncate text-control ${disabled ? "text-muted-foreground" : "text-foreground"}`}>{label}</span>
			{statusIndicator === "auth-unknown" ? (
				<TooltipProvider>
					<Tooltip open={tooltipOpen} onOpenChange={setTooltipOpen}>
						<TooltipTrigger asChild>
							<span className="shrink-0 text-settings-muted" role="img" aria-label={t("agentSelector.authUnknown")}>
								<CircleAlert className="size-3.5" aria-hidden="true" />
							</span>
						</TooltipTrigger>
						<TooltipContent>{t("agentSelector.authUnknown")}</TooltipContent>
					</Tooltip>
				</TooltipProvider>
			) : status ? (
				<span className={cn("shrink-0 text-caption", STATUS_TONE_CLASS[statusTone ?? "muted"])}>{status}</span>
			) : null}
			{selected ? <Check className="size-3 shrink-0 text-settings-label" aria-hidden="true" /> : null}
		</span>
	);
}
