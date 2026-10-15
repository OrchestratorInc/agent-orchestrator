import { Search } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { labelForHost } from "../../lib/host-clients";
import { LOCAL_HOST } from "../../lib/hosts";
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs";
import { CloudHarnessContent } from "./harness/CloudHarnessContent";
import { LocalHarnessContent } from "./harness/LocalHarnessContent";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsSection } from "./SettingsSection";

export type HarnessView = "local" | "cloud";

export function HarnessSettingsSection({
	focusAgentId,
	hostId,
	initialView = "local",
	startLogin = false,
	titleHidden = false,
}: {
	focusAgentId?: string;
	hostId?: string;
	initialView?: HarnessView;
	/** Start focusAgentId's local login flow once its row is ready (a shortcut from a login error elsewhere). */
	startLogin?: boolean;
	titleHidden?: boolean;
}) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const [view, setView] = useState<HarnessView>(initialView);
	const [search, setSearch] = useState("");
	useEffect(() => setView(initialView), [initialView]);
	const cloudView = cloudEnabled && view === "cloud";
	const connected = useConnectedHosts();
	const [selectedHostId, setSelectedHostId] = useState(hostId ?? LOCAL_HOST);
	useEffect(() => setSelectedHostId(hostId ?? LOCAL_HOST), [hostId]);
	const remoteOffline = selectedHostId !== LOCAL_HOST && !connected.includes(selectedHostId);
	// An open harness page replaces the list, so its search and filters step aside.
	const [detailOpen, setDetailOpen] = useState(false);
	const showToolbar = cloudView || !detailOpen;
	return <SettingsSection title={t("settings.harness")} titleHidden={titleHidden} sectionId="harness">
		{showToolbar ? <div className="sticky top-0 z-10 -mt-[18px] flex items-center gap-2 bg-(--color-bg-primary) pb-2 pt-[18px]">
			<label className="flex h-9! min-w-0 flex-1 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3">
				<Search aria-hidden="true" className="size-4 shrink-0 text-settings-muted" />
				<span className="sr-only">{t("settings.harness.search")}</span>
				<input aria-label={t("settings.harness.search")} className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted" placeholder={t("settings.harness.searchPlaceholder")} value={search} onChange={(event) => setSearch(event.target.value)} />
			</label>
			{cloudEnabled ? <Tabs value={cloudView ? "cloud" : "local"} onValueChange={(value) => setView(value as HarnessView)}><TabsList aria-label={t("settings.harness.viewLabel")}><TabsTrigger value="local">{t("settings.harness.viewLocal")}</TabsTrigger><TabsTrigger value="cloud">{t("settings.harness.viewCloud")}</TabsTrigger></TabsList></Tabs> : null}
		</div> : null}
		{showToolbar && !cloudView && (connected.length > 0 || remoteOffline) ? <SettingsOptionMenu
				aria-label={t("remote.host")}
				value={selectedHostId}
				options={[{ value: LOCAL_HOST, label: t("settings.harness.thisComputer") }, ...connected.map((id) => ({ value: id, label: labelForHost(id) ?? id })), ...(remoteOffline ? [{ value: selectedHostId, label: t("remote.hostLabel", { hostId: selectedHostId }) }] : [])]}
				onChange={setSelectedHostId}
				triggerClassName="w-fit max-w-full"
			/> : null}
		{showToolbar && !cloudView && selectedHostId !== LOCAL_HOST && !remoteOffline ? <p className="text-xs text-muted-foreground">{t("settings.harness.remoteBrowserAuthNote")}</p> : null}
		{cloudView ? <CloudHarnessContent focusAgentId={focusAgentId} search={search} /> : remoteOffline ? <p className="text-xs text-error" role="alert">{t("remote.hostOffline")}</p> : <LocalHarnessContent key={selectedHostId} focusAgentId={focusAgentId} hostId={selectedHostId === LOCAL_HOST ? undefined : selectedHostId} search={search} onDetailChange={setDetailOpen} startLogin={startLogin && selectedHostId === (hostId ?? LOCAL_HOST)} />}
	</SettingsSection>;
}
