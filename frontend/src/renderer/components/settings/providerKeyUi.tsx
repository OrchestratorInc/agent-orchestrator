import { Info, TriangleAlert } from "@/components/app-icons";
import { useEffect, useState, type ReactNode } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { ProviderKey } from "../../hooks/useProviderKeys";
import type { AgentId } from "../../lib/agent-options";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";

export const REMOVE_CONFIRM_MS = 4_000;

export function savedKeySummary(t: TFunction, key?: ProviderKey): string | undefined {
	if (!key?.configured) return undefined;
	return [
		key.hint ? t("settings.apiKeys.savedHint", { hint: key.hint }) : t("settings.apiKeys.saved"),
		key.device ? t("settings.apiKeys.outsideAo") : "",
	].filter(Boolean).join(" · ");
}

export function providerKeyNeedsModel(agentId: AgentId, methodId?: string): boolean {
	if (!methodId?.startsWith("env_")) return false;
	return agentId === "qwen" || agentId === "openhands" || (agentId === "continue" && methodId !== "env_CONTINUE_API_KEY");
}

export const KEY_FIELD_CLASS = "h-control-form rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 font-mono text-control text-settings-label placeholder:font-mono placeholder:text-settings-muted focus-visible:ring-2 focus-visible:ring-ring/30";

export function DeviceEnvToggle({ checked, disabled, onChange, className }: {
	checked: boolean;
	disabled?: boolean;
	onChange: (checked: boolean) => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const [hintOpen, setHintOpen] = useState(false);
	const hint = t("settings.apiKeys.deviceHint");
	useEffect(() => {
		if (!hintOpen) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
			event.stopPropagation();
			setHintOpen(false);
		};
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [hintOpen]);
	return <div className={cn("flex min-w-0 items-center gap-1", className)}>
		<label className="flex w-fit cursor-pointer items-center gap-2 has-disabled:cursor-default">
			<Checkbox checked={checked} disabled={disabled} onCheckedChange={(next) => onChange(next === true)} />
			<span className="text-control leading-5 text-settings-label">{t("settings.apiKeys.device")}</span>
		</label>
		<TooltipProvider>
			<Tooltip open={hintOpen} onOpenChange={setHintOpen}>
				<TooltipTrigger asChild>
					<Button type="button" size="icon-xs" variant="quiet" className="-my-1" aria-label={hint}
						onPointerDown={(event) => event.preventDefault()}
						onClick={(event) => { event.preventDefault(); setHintOpen(true); }}>
						<Info className="size-3.5" aria-hidden="true" />
					</Button>
				</TooltipTrigger>
				<TooltipContent className="max-w-72">{hint}</TooltipContent>
			</Tooltip>
		</TooltipProvider>
	</div>;
}

export function KeyWarning({ children, className }: { children: ReactNode; className?: string }) {
	return <p className={cn("flex items-start gap-1.5 text-xs leading-4 text-settings-label", className)}>
		<TriangleAlert className="size-3.5 shrink-0 text-warning" aria-hidden="true" />
		<span>{children}</span>
	</p>;
}
