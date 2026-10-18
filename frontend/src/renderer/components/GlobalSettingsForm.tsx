import { Fragment, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { type GlobalSettingsSection as GlobalSettingsPage, type HarnessDiagnosticsRequest, useUiStore } from "../stores/ui-store";
import { globalSettingsItemsFor } from "./settings/settingsCatalog";
import { PromptOverrideDialog } from "./settings/PromptOverrideDialog";
import { SettingsPageTitleContext, SettingsSection } from "./settings/SettingsSection";

export type GlobalSettingsSection = GlobalSettingsPage | "all";

export function GlobalSettingsForm({
	cloudEnabled = true,
	customCoder = false,
	selfServeBilling = false,
	focusAgentId,
	hostId,
	harnessView,
	startLogin,
	showDiagnostics,
	section = "all",
}: {
	cloudEnabled?: boolean;
	/** The org's offering includes coder (SettingsDialog reads useCloudOffering). */
	customCoder?: boolean;
	/** The org is on self-serve billing. */
	selfServeBilling?: boolean;
	focusAgentId?: string;
	hostId?: string;
	harnessView?: "local" | "cloud";
	startLogin?: boolean;
	showDiagnostics?: HarnessDiagnosticsRequest;
	section?: GlobalSettingsSection;
}) {
	const { t } = useTranslation();
	const [agentDefaultsOpen, setAgentDefaultsOpen] = useState(false);
	const developerMode = useUiStore((state) => state.developerMode);
	const diagnostics = useUiStore((state) => state.developerMode && state.diagnostics);
	const all = section === "all";
	const context = { cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling, focusAgentId, hostId, harnessView, startLogin, showDiagnostics, singlePage: !all };
	const items = globalSettingsItemsFor(section, context);
	// On a single page the pane's title names it; SettingsSection hides only a
	// heading that would repeat that title, so other section headings show.
	const pageTitle = all ? null : (items[0]?.label(t) ?? null);
	const titleHidden = !all;
	// Fork: user-scope prompt override ("Agent Defaults") stays a standalone
	// panel ahead of the catalog's general section, preserving the original
	// section order on both the "all" and "general" pages.

	return (
		<SettingsPageTitleContext.Provider value={pageTitle}>
			<div
				aria-label={t("settings.title")}
				className="flex w-full flex-col gap-(--size-settings-section-gap)"
			>
				{(all || section === "general") && (
					<SettingsSection title={t("settings.agentDefaults")} titleHidden={titleHidden}>
						<button
							type="button"
							className="w-full rounded-md bg-[var(--color-bg-settings-row)] px-4 py-3 text-left"
							onClick={() => setAgentDefaultsOpen(true)}
						>
							{t("settings.agentDefaults")}
						</button>
					</SettingsSection>
				)}
				{items.map((item) => (
					<Fragment key={item.id}>
						<Suspense fallback={null}>{item.render(t, false, context)}</Suspense>
					</Fragment>
				))}
			</div>
			<PromptOverrideDialog open={agentDefaultsOpen} onOpenChange={setAgentDefaultsOpen} scope="user" />
		</SettingsPageTitleContext.Provider>
	);
}
