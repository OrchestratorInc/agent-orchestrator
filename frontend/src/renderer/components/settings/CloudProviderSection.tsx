import { Server } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudProviderPreference } from "../../hooks/useCloudProviderPreference";
import { useCloudSandboxProviders } from "../../hooks/useCloudSandboxProviders";
import { useCloudSession } from "../../lib/cloud-session";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

// Proper nouns; deliberately not translated (matches CloudCredentialsSection's
// AGENT_LABELS convention).
const PROVIDER_LABELS: Record<string, string> = {
	nodeops: "NodeOps",
	coder: "Coder",
	docker: "Docker",
	ecs: "ECS",
	daytona: "Daytona",
};

function providerLabel(provider: string): string {
	return PROVIDER_LABELS[provider] ?? provider;
}

/**
 * Sandbox provider selector in global settings. Like CloudCredentialsSection,
 * the outer component only reads the daemon cloud gate (a query the settings
 * page already runs), so a local-only app renders nothing and never mounts the
 * cloud hooks; the inner component subscribes to the cloud session/provider
 * queries. The selection is stored on the Cloud account and applied by the
 * control plane when a new session does not explicitly choose a provider.
 */
export function CloudProviderSection({ titleHidden }: { titleHidden?: boolean }) {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <CloudProviderSectionInner titleHidden={titleHidden} />;
}

function CloudProviderSectionInner({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { status } = useCloudSession();
	const { available, default: defaultProvider } = useCloudSandboxProviders();
	const { provider, loading, saving, error, setProvider } = useCloudProviderPreference();
	const savedProviderUnavailable = provider !== null && !available.includes(provider);

	// Choosing a provider needs the signed-in session that reports what the
	// control plane offers. The Cloud settings page is reachable while signed
	// out, so say why it is empty instead of rendering a blank pane.
	if (status !== "authenticated") {
		return (
			<SettingsSection title={t("settings.cloudProvider")} sectionId="cloud-provider" titleHidden={titleHidden}>
				<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudProvider.signIn")}</p>
			</SettingsSection>
		);
	}

	// A control plane that offers a single provider (or predates multi-provider)
	// leaves nothing to choose, so show it read-only rather than a one-item menu.
	if (available.length <= 1 && !savedProviderUnavailable) {
		const only = available[0] ?? defaultProvider;
		return (
			<SettingsSection title={t("settings.cloudProvider")} sectionId="cloud-provider" titleHidden={titleHidden}>
				<SettingsRow icon={Server} label={t("settings.cloudProvider.label")}>
					<span className="text-sm leading-5 text-settings-muted">
						{only ? providerLabel(only) : t("settings.cloudProvider.none")}
					</span>
				</SettingsRow>
				{error ? <p role="alert" className="px-3 text-xs text-destructive">{error}</p> : null}
				<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudProvider.description")}</p>
			</SettingsSection>
		);
	}

	// Fall back to the control plane default only for presentation when no
	// available saved choice exists. The server still rejects creation if a
	// previously saved provider has become unavailable.
	const effective = provider && available.includes(provider) ? provider : defaultProvider;
	const options: SettingsOption<string>[] = available.map((provider) => ({
		value: provider,
		label: providerLabel(provider),
	}));

	return (
		<SettingsSection title={t("settings.cloudProvider")} sectionId="cloud-provider" titleHidden={titleHidden}>
			<SettingsRow icon={Server} label={t("settings.cloudProvider.label")}>
				<SettingsOptionMenu
					aria-label={t("settings.cloudProvider.label")}
					value={effective}
					options={options}
					disabled={loading || saving}
					onChange={(next) => { void setProvider(next); }}
				/>
			</SettingsRow>
			{savedProviderUnavailable ? (
				<p role="alert" className="px-3 text-xs text-destructive">
					{t("settings.cloudProvider.unavailable", { provider: providerLabel(provider ?? "") })}
				</p>
			) : null}
			{error ? <p role="alert" className="px-3 text-xs text-destructive">{error}</p> : null}
			<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudProvider.description")}</p>
		</SettingsSection>
	);
}
