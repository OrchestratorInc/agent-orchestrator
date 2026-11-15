import { BrowserIcon, type AppIcon } from "../icons";
import { Activity, Bot, CircleHelp, CreditCard, Keyboard, MousePointerClick, RefreshCw, Server, Settings2, Smartphone, Users } from "@/components/app-icons";
import { lazy, type ReactNode } from "react";
import type { TFunction } from "i18next";
import type { GlobalSettingsSection, HarnessDiagnosticsRequest } from "../../stores/ui-store";
import { BillingSection } from "./BillingSection";
import { BrowserChromeSection } from "./BrowserChromeSection";
import { BrowserDownloadsSection } from "./BrowserDownloadsSection";
import { BrowserProfilesSection } from "./BrowserProfilesSection";
import { CoderSettingsSection } from "./CoderSettingsSection";
import { ComputerUseSection } from "./ComputerUseSection";
import { ConnectMobileContent } from "./ConnectMobileContent";
import { GeneralSettingsSection } from "./GeneralSettingsSection";
import { HarnessSettingsSection } from "./HarnessSettingsSection";
import { KeyboardShortcutsContent } from "./KeyboardShortcutsContent";
import { MobileDevicesSection } from "./MobileDevicesSection";
import { ReportProblemContent } from "./ReportProblemContent";
import { RemoteHostsSettings } from "./RemoteHostsSettings";
import { SettingsSection } from "./SettingsSection";
import { teamsUnavailable } from "../../hooks/useTeams";
import { TeamsSection } from "./TeamsSection";

const MemoryDiagnostics = lazy(async () => {
	const module = await import("../SessionMemoryPanel");
	return { default: module.MemoryDiagnostics };
});

const UpdatesSection = lazy(async () => {
	const module = await import("./UpdatesSection");
	return { default: module.UpdatesSection };
});

export type TeamsPage = "manage" | "member" | "none";

export function teamsPageFor({ share, orgId, mine }: {
	share: boolean;
	orgId: string | undefined;
	mine: { isPending: boolean; error: unknown; data?: Array<{ orgId: string }> };
}): TeamsPage {
	if (mine.isPending || teamsUnavailable(mine.error)) return "none";
	if (share) return "manage";
	return (mine.data ?? []).some((team) => team.orgId !== orgId) ? "member" : "none";
}

type CatalogContext = {
	cloudEnabled: boolean;
	diagnostics: boolean;
	customCoder: boolean;
	selfServeBilling: boolean;
	teams?: TeamsPage;
	focusAgentId?: string;
	hostId?: string;
	harnessView?: "local" | "cloud";
	startLogin?: boolean;
	showDiagnostics?: HarnessDiagnosticsRequest;
	singlePage?: boolean;
};

export type SettingsCatalogItem = {
	id: GlobalSettingsSection;
	icon: AppIcon;
	label: (t: TFunction) => string;
	visible?: (context: CatalogContext) => boolean;
	pageOnly?: boolean;
	render: (t: TFunction, titleHidden: boolean, context: CatalogContext) => ReactNode;
};

const globalSettingsCatalog: SettingsCatalogItem[] = [
	{
		id: "general",
		icon: Settings2,
		label: (t) => t("settings.general"),
		render: (_t, titleHidden) => <GeneralSettingsSection titleHidden={titleHidden} />,
	},
	{
		id: "harness",
		icon: Bot,
		label: (t) => t("settings.harness"),
		render: (_t, titleHidden, { focusAgentId, hostId, harnessView, startLogin, showDiagnostics }) => <HarnessSettingsSection focusAgentId={focusAgentId} {...(hostId ? { hostId } : {})} {...(harnessView ? { initialView: harnessView } : {})} {...(startLogin ? { startLogin } : {})} {...(showDiagnostics ? { showDiagnostics } : {})} titleHidden={titleHidden} />,
	},
	{
		id: "teams",
		icon: Users,
		label: (t) => t("settings.teams"),
		visible: ({ cloudEnabled, teams }) => cloudEnabled && (teams === "manage" || teams === "member"),
		pageOnly: true,
		render: (_t, titleHidden, { teams }) => <TeamsSection titleHidden={titleHidden} manage={teams === "manage"} />,
	},
	{
		id: "billing",
		icon: CreditCard,
		label: (t) => t("settings.billing.navLabel"),
		visible: ({ cloudEnabled, selfServeBilling }) => cloudEnabled && selfServeBilling,
		render: (_t, titleHidden, { singlePage }) => <BillingSection titleHidden={titleHidden || Boolean(singlePage)} />,
	},
	{
		id: "browserProfiles",
		icon: BrowserIcon,
		label: (t) => t("settings.browserProfiles"),
		render: (_t, titleHidden) => (
			<>
				<BrowserProfilesSection titleHidden={titleHidden} />
				<BrowserDownloadsSection />
				<BrowserChromeSection />
			</>
		),
	},
	{
		id: "computerUse",
		icon: MousePointerClick,
		label: (t) => t("settings.computerUse"),
		render: (_t, titleHidden) => <ComputerUseSection titleHidden={titleHidden} />,
	},
	{
		id: "coder",
		icon: Server,
		label: (t) => t("settings.coder.navLabel"),
		visible: ({ cloudEnabled, customCoder }) => cloudEnabled && customCoder,
		render: (_t, titleHidden, { singlePage }) => <CoderSettingsSection titleHidden={titleHidden || Boolean(singlePage)} />,
	},
	{
		id: "remoteHosts",
		icon: Server,
		label: (t) => t("settings.remoteHosts"),
		render: (_t, titleHidden) => <RemoteHostsSettings titleHidden={titleHidden} />,
	},
	{
		id: "mobile",
		icon: Smartphone,
		label: (t) => t("settings.mobile"),
		render: (t, titleHidden) => (
			<>
				<SettingsSection titleHidden={titleHidden} title={t("settings.mobile")} grouped>
					<ConnectMobileContent active />
				</SettingsSection>
				<MobileDevicesSection />
			</>
		),
	},
	{
		id: "shortcuts",
		icon: Keyboard,
		label: (t) => t("settings.shortcuts"),
		render: (_t, titleHidden, { singlePage }) => <KeyboardShortcutsContent active bounded={Boolean(singlePage)} titleHidden={titleHidden || Boolean(singlePage)} />,
	},
	{
		id: "diagnostics",
		icon: Activity,
		label: (t) => t("settings.diagnostics"),
		pageOnly: true,
		visible: ({ diagnostics }) => diagnostics,
		render: (t, titleHidden) => (
			<SettingsSection titleHidden={titleHidden} title={t("settings.diagnostics")}>
				<MemoryDiagnostics />
			</SettingsSection>
		),
	},
	{
		id: "updates",
		icon: RefreshCw,
		label: (t) => t("settings.updates"),
		render: (_t, titleHidden) => <UpdatesSection titleHidden={titleHidden} />,
	},
	{
		id: "help",
		icon: CircleHelp,
		label: (t) => t("settings.help"),
		render: (t, titleHidden) => (
			<SettingsSection titleHidden={titleHidden} title={t("settings.reportProblem")} grouped>
				<div className="settings-content-row">
					<ReportProblemContent active />
				</div>
			</SettingsSection>
		),
	},
];

export function visibleGlobalSettings(context: CatalogContext): SettingsCatalogItem[] {
	return globalSettingsCatalog.filter((item) => item.visible?.(context) ?? true);
}

export function globalSettingsItem(section: GlobalSettingsSection, context: CatalogContext): SettingsCatalogItem {
	return visibleGlobalSettings(context).find((item) => item.id === section) ?? globalSettingsCatalog[0];
}

export function globalSettingsItemsFor(section: GlobalSettingsSection | "all", context: CatalogContext): SettingsCatalogItem[] {
	return section === "all"
		? visibleGlobalSettings(context).filter((item) => !item.pageOnly)
		: [globalSettingsItem(section, context)];
}
