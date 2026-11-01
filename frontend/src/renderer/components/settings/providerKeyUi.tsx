import { TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { ProviderKey } from "../../hooks/useProviderKeys";
import type { AgentId } from "../../lib/agent-options";
import { cn } from "../../lib/utils";
import { Checkbox } from "../ui/checkbox";

// Pieces shared by the API keys page and a harness's Account login panel, so
// a saved key reads and behaves the same on both.

/** "Saved, ending 1234 · Also set outside AO", or undefined while no key is saved. */
export function savedKeySummary(t: TFunction, key?: ProviderKey): string | undefined {
	if (!key?.configured) return undefined;
	return [
		key.hint ? t("settings.apiKeys.savedHint", { hint: key.hint }) : t("settings.apiKeys.saved"),
		key.device ? t("settings.apiKeys.outsideAo") : "",
	].filter(Boolean).join(" · ");
}

/** Harnesses where a key alone can't pick a working provider, so a model ID is required too. */
export function providerKeyNeedsModel(agentId: AgentId, methodId?: string): boolean {
	if (!methodId?.startsWith("env_")) return false;
	return agentId === "qwen" || agentId === "openhands" || (agentId === "continue" && methodId !== "env_CONTINUE_API_KEY");
}

/** A small dot beside a key's name: filled once it is saved. Decorative; the row text carries the state. */
export function KeyStatusDot({ configured }: { configured: boolean }) {
	return <span
		aria-hidden="true"
		data-key-status={configured ? "saved" : "unset"}
		className={cn("block size-2 rounded-full", configured ? "bg-success" : "border border-settings-muted/60")}
	/>;
}

/**
 * Opt-in to publish a key to the OS user environment. Bordered so it reads as
 * a separate decision from typing the key, with its consequence stated.
 */
export function DeviceEnvToggle({ checked, disabled, onChange, className }: {
	checked: boolean;
	disabled?: boolean;
	onChange: (checked: boolean) => void;
	className?: string;
}) {
	const { t } = useTranslation();
	return <label className={cn("flex cursor-pointer items-start gap-2.5 rounded-md border border-border px-3 py-2.5 has-disabled:cursor-default", className)}>
		<Checkbox checked={checked} className="mt-0.5" disabled={disabled} onCheckedChange={(next) => onChange(next === true)} />
		<span className="min-w-0 flex-1 space-y-0.5">
			<span className="block text-control leading-5 text-settings-label">{t("settings.apiKeys.device")}</span>
			<span className="block text-xs leading-4 text-settings-muted">{t("settings.apiKeys.deviceHint")}</span>
		</span>
	</label>;
}

/** A consequence the user should read before saving, such as billing. */
export function KeyWarning({ children, className }: { children: ReactNode; className?: string }) {
	return <p className={cn("flex items-start gap-2 rounded-md border border-warning/30 bg-warning/8 px-3 py-2.5 text-xs leading-4 text-settings-label", className)}>
		<TriangleAlert className="mt-px size-3.5 shrink-0 text-warning" aria-hidden="true" />
		<span>{children}</span>
	</p>;
}
