import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudOffering } from "../../hooks/useCloudOffering";
import { cloudMeQueryKey } from "../../lib/cloud-offering";
import { cn } from "../../lib/utils";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

/**
 * Where the org's cloud sessions run when a project does not pick a provider,
 * and which provider new projects start on. Every org may run on every provider
 * the deployment offers and picks its default here: an org with the coder
 * capability (WorkOS metadata capabilities=coder) starts on Coder, every other
 * org on Freestyle. On a deployment with a single provider the org sees it,
 * fixed. The control plane stores the choice per org and applies it to every
 * session it places, from the desktop, mobile or `ao spawn`.
 */
export function DefaultCloudSandboxRow() {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <DefaultCloudSandboxRowInner />;
}

function DefaultCloudSandboxRowInner() {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const offering = useCloudOffering();
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const orgId = offering.org?.id ?? "";
	if (offering.providers.length === 0 || orgId === "") return null;
	const choice = offering.offersProviderChoice;

	const options = offering.providers.map((provider) => ({
		value: provider,
		label: t(`createProject.sandboxProvider.${provider}`, { defaultValue: provider }),
	})) satisfies SettingsOption<string>[];
	const change = async (provider: string) => {
		if (provider === offering.defaultProvider) return;
		setSaving(true);
		setError(null);
		try {
			await client.putOrgSandboxProvider(orgId, provider);
			await queryClient.invalidateQueries({ queryKey: cloudMeQueryKey });
		} catch {
			setError(t("settings.defaultSandbox.saveFailed"));
		} finally {
			setSaving(false);
		}
	};
	const note = error ?? (choice && !offering.canManageBilling ? t("settings.defaultSandbox.adminOnly") : null);

	return (
		<div className="flex w-full flex-col">
			<SettingsRow
				className="rounded-none"
				label={t("settings.defaultSandbox.label")}
				description={t(choice ? "settings.defaultSandbox.description" : "settings.defaultSandbox.single")}
			>
				<SettingsOptionMenu
					aria-label={t("settings.defaultSandbox.label")}
					value={offering.defaultProvider || offering.providers[0]}
					options={options}
					onChange={(provider) => void change(provider)}
					disabled={saving || !choice || !offering.canManageBilling}
				/>
			</SettingsRow>
			{note ? (
				<p
					role={error ? "alert" : undefined}
					className={cn("px-3 pt-0 pb-4 text-xs leading-relaxed", error ? "text-destructive" : "text-muted-foreground")}
				>
					{note}
				</p>
			) : null}
		</div>
	);
}
