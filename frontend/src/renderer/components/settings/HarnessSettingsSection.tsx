import { Search } from "lucide-react";
import { useEffect, useState } from "react";
import { RadioGroup } from "radix-ui";
import { useTranslation } from "react-i18next";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { labelForHost } from "../../lib/host-clients";
import { LOCAL_HOST } from "../../lib/hosts";
import type { HarnessDiagnosticsRequest } from "../../stores/ui-store";
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
	showDiagnostics,
	titleHidden = false,
}: {
	focusAgentId?: string;
	hostId?: string;
	initialView?: HarnessView;
	/** Start focusAgentId's local login flow once its row is ready (a shortcut from a login error elsewhere). */
	startLogin?: boolean;
	/** Open focusAgentId's local install or update diagnostics (a shortcut from a failed update notice). */
	showDiagnostics?: HarnessDiagnosticsRequest;
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
	return <SettingsSection
		title={t("settings.harness")}
		titleHidden={titleHidden}
		sectionId="harness"
		// The search sits under the page heading, pulled up by SettingsSection when that heading is hidden.
		toolbar={showToolbar ? (<div className="sticky top-toolbar z-10 flex items-center gap-2 bg-(--color-bg-primary) pb-2">
			<label className="flex h-9! min-w-0 flex-1 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3">
				<Search aria-hidden="true" className="size-4 shrink-0 text-settings-muted" />
				<span className="sr-only">{t("settings.harness.search")}</span>
				<input aria-label={t("settings.harness.search")} className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted" placeholder={t("settings.harness.searchPlaceholder")} value={search} onChange={(event) => setSearch(event.target.value)} />
			</label>
			{cloudEnabled ? <RadioGroup.Root value={cloudView ? "cloud" : "local"} onValueChange={(value) => setView(value as HarnessView)} aria-label={t("settings.harness.viewLabel")} className="settings-segment rounded-md">
				<RadioGroup.Item value="local" className="settings-segment-item rounded-md">{t("settings.harness.viewLocal")}</RadioGroup.Item>
				<RadioGroup.Item value="cloud" className="settings-segment-item rounded-md">{t("settings.harness.viewCloud")}</RadioGroup.Item>
			</RadioGroup.Root> : null}
		</div>) : null}
	>
		{showToolbar && !cloudView && (connected.length > 0 || remoteOffline) ? <SettingsOptionMenu
				aria-label={t("remote.host")}
				value={selectedHostId}
				options={[{ value: LOCAL_HOST, label: t("settings.harness.thisComputer") }, ...connected.map((id) => ({ value: id, label: labelForHost(id) ?? id })), ...(remoteOffline ? [{ value: selectedHostId, label: t("remote.hostLabel", { hostId: selectedHostId }) }] : [])]}
				onChange={setSelectedHostId}
				triggerClassName="w-fit max-w-full"
			/> : null}
		{showToolbar && !cloudView && selectedHostId !== LOCAL_HOST && !remoteOffline ? <p className="text-xs text-muted-foreground">{t("settings.harness.remoteBrowserAuthNote")}</p> : null}
		{cloudView ? <CloudHarnessContent focusAgentId={focusAgentId} search={search} /> : remoteOffline ? <p className="text-xs text-error" role="alert">{t("remote.hostOffline")}</p> : <LocalHarnessContent key={selectedHostId} focusAgentId={focusAgentId} hostId={selectedHostId === LOCAL_HOST ? undefined : selectedHostId} search={search} onDetailChange={setDetailOpen} startLogin={startLogin && selectedHostId === (hostId ?? LOCAL_HOST)} showDiagnostics={selectedHostId === (hostId ?? LOCAL_HOST) ? showDiagnostics : undefined} />}
	</SettingsSection>;
}
