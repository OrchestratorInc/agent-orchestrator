import { Fragment, Suspense } from "react";
import { useTranslation } from "react-i18next";
import { type GlobalSettingsSection as GlobalSettingsPage, useUiStore } from "../stores/ui-store";
import { globalSettingsItemsFor } from "./settings/settingsCatalog";

export type GlobalSettingsSection = GlobalSettingsPage | "all";

export function GlobalSettingsForm({
	cloudEnabled = true,
	is11x = false,
	focusAgentId,
	hostId,
	harnessView,
	startLogin,
	section = "all",
}: {
	cloudEnabled?: boolean;
	is11x?: boolean;
	focusAgentId?: string;
	hostId?: string;
	harnessView?: "local" | "cloud";
	startLogin?: boolean;
	section?: GlobalSettingsSection;
}) {
	const { t } = useTranslation();
	const developerMode = useUiStore((state) => state.developerMode);
	const diagnostics = useUiStore((state) => state.developerMode && state.diagnostics);
	const all = section === "all";
	const context = { cloudEnabled, developerMode, diagnostics, is11x, focusAgentId, hostId, harnessView, startLogin };
	// One section per page means the dialog header already names it, so a
	// leading in-page heading would just repeat that title.
	const titleHidden = !all;

	return (
		<div
			aria-label={t("settings.title")}
			className="flex w-full flex-col gap-(--size-settings-section-gap)"
		>
			{globalSettingsItemsFor(section, context).map((item) => (
				<Fragment key={item.id}>
					<Suspense fallback={null}>{item.render(t, titleHidden, context)}</Suspense>
				</Fragment>
			))}
		</div>
	);
}
