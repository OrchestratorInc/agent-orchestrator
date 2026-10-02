import { useId } from "react";
import { useTranslation } from "react-i18next";
import { AccountsManagerSection } from "./AccountsManagerSection";
import { AccountRemovalRecovery } from "./AccountRemovalControl";
import { CodexAccountsSection } from "./CodexAccountsSection";
import { SettingsSection } from "./SettingsSection";

export function AccountsSettingsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const managedHeading = useId();
	const deviceHeading = useId();
	return (
		<SettingsSection title={t("accountsManager.title")} titleHidden={titleHidden} sectionId="accounts">
			<div className="space-y-6">
				<section aria-labelledby={managedHeading} className="space-y-3">
					<div className="space-y-1">
						<h3 id={managedHeading} className="text-sm font-medium text-foreground">{t("accountsManager.sessionAccounts")}</h3>
						<p className="text-xs text-muted-foreground">{t("accountsManager.sessionAccountsDescription")}</p>
					</div>
					<AccountsManagerSection titleHidden />
					<AccountRemovalRecovery />
				</section>
				<section aria-labelledby={deviceHeading} className="space-y-3 border-t border-border/60 pt-5">
					<div className="space-y-1">
						<h3 id={deviceHeading} className="text-sm font-medium text-foreground">{t("accountsManager.deviceAccounts")}</h3>
						<p className="text-xs text-muted-foreground">{t("accountsManager.deviceAccountsDescription")}</p>
					</div>
					<CodexAccountsSection titleHidden />
				</section>
			</div>
		</SettingsSection>
	);
}
