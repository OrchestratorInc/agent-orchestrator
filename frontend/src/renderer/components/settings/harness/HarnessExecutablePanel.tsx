import { useMutation } from "@tanstack/react-query";
import { FolderOpen, LoaderCircle } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { useSettings, useUpdateHarnessDefault } from "../../../hooks/useSettings";
import type { AgentId } from "../../../lib/agent-options";
import { apiErrorMessage } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { clientForSessionHost, labelForHost } from "../../../lib/host-clients";
import { LOCAL_HOST } from "../../../lib/hosts";
import { RemoteDirectoryPicker } from "../../RemoteDirectoryPicker";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { HarnessDetailGroup, HarnessDetailRow } from "./harness-ui";

interface HarnessExecutablePanelProps {
	agentId: AgentId;
	hostId?: string;
	executable?: string;
	version?: string;
	busy?: boolean;
}

/** 主机或 Harness 改变时丢弃尚未应用的选择，避免把旧扫描结果保存到另一主机。 */
export function HarnessExecutablePanel(props: HarnessExecutablePanelProps) {
	if (props.agentId === "unreal-agent") return null;
	return <ExecutableSelection key={`${props.hostId ?? LOCAL_HOST}:${props.agentId}`} {...props} />;
}

/** 扫描只产生候选；应用成功才更新守护进程保存的 CLI 来源。 */
function ExecutableSelection({ agentId, hostId, executable, version, busy = false }: HarnessExecutablePanelProps) {
	const { t } = useTranslation();
	const directoryId = useId();
	const { settings, error: settingsError } = useSettings(hostId);
	const saved = settings?.harnessDefaults?.[agentId];
	const update = useUpdateHarnessDefault(hostId);
	const [directory, setDirectory] = useState<string | null>(null);
	const [selectedPath, setSelectedPath] = useState("");
	const [remoteBrowse, setRemoteBrowse] = useState(false);
	const [choosing, setChoosing] = useState(false);
	const [actionError, setActionError] = useState<string>();
	const [savedMessage, setSavedMessage] = useState(false);
	const inputDirectory = directory ?? saved?.executableDirectory ?? "";
	const scan = useMutation({
		mutationFn: async (): Promise<components["schemas"]["AgentExecutableScanResponse"]> => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/executables/scan", {
				params: { path: { agent: agentId } }, body: { directory: inputDirectory.trim() },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, t("settings.harness.executable.scanFailed")));
			return data;
		},
		onSuccess: (result) => {
			const [candidate] = result.candidates;
			setSelectedPath(result.candidates.length === 1 && !candidate.error ? candidate.path : "");
		},
	});
	const disabled = busy || !settings || scan.isPending || update.saving || choosing;
	const currentPath = saved?.executablePath || executable;
	const error = actionError || (scan.error ? apiErrorMessage(scan.error) : undefined) || settingsError;

	/** 修改搜索目录使旧候选失效，但保留当前已保存的来源。 */
	function changeDirectory(value: string) {
		setDirectory(value);
		setSelectedPath("");
		scan.reset();
		setActionError(undefined);
		setSavedMessage(false);
	}

	/** 远程目录由对应守护进程浏览，本机目录交给桌面原生选择器。 */
	async function browse() {
		if (hostId) {
			setRemoteBrowse(true);
			return;
		}
		setChoosing(true);
		setActionError(undefined);
		try {
			const chosen = await aoBridge.app.chooseDirectory({ title: t("settings.harness.executable.directory"), defaultPath: inputDirectory || undefined });
			if (chosen) changeDirectory(chosen);
		} catch (error) {
			setActionError(apiErrorMessage(error, t("settings.harness.executable.browseFailed")));
		} finally {
			setChoosing(false);
		}
	}

	/** 失败保留已保存来源；恢复自动通过两个空字段清除指定路径。 */
	async function save(automatic: boolean) {
		if (!automatic && (!scan.data || !selectedPath)) return;
		setActionError(undefined);
		setSavedMessage(false);
		try {
			await update.update({ agentId, executableDirectory: automatic ? "" : scan.data!.directory, executablePath: automatic ? "" : selectedPath });
			setDirectory(null);
			setSelectedPath("");
			scan.reset();
			setSavedMessage(true);
		} catch (error) {
			setActionError(apiErrorMessage(error, t("settings.harness.executable.saveFailed")));
		}
	}

	return <HarnessDetailGroup title={t("settings.harness.executable.title")}>
		<HarnessDetailRow label={t("settings.harness.executable.source")}>
			<span className="text-xs text-settings-label">{t(!settings ? "common.loading" : saved?.executablePath ? "settings.harness.executable.specified" : "settings.harness.executable.automatic")}</span>
		</HarnessDetailRow>
		{currentPath ? <HarnessDetailRow label={t("settings.harness.health.executable")}>
			<span className="break-all font-mono text-xs text-settings-label">{currentPath}</span>
		</HarnessDetailRow> : null}
		{version ? <HarnessDetailRow label={t("settings.harness.installedVersion")}>
			<span className="font-mono text-xs text-settings-label">{version}</span>
		</HarnessDetailRow> : null}
		<div className="flex flex-col gap-3 p-3">
			<p className="text-xs text-settings-muted">{t("settings.harness.executable.description")}</p>
			<label className="text-sm text-settings-label" htmlFor={directoryId}>{t("settings.harness.executable.directory")}</label>
			<div className="flex flex-wrap gap-2">
				<Input id={directoryId} className="min-w-0 flex-1 basis-48 font-mono" value={inputDirectory} disabled={disabled} onChange={(event) => changeDirectory(event.target.value)} />
				<Button type="button" variant="outline" size="sm" disabled={disabled} onClick={() => void browse()}><FolderOpen aria-hidden="true" />{t("settings.harness.executable.browse")}</Button>
				<Button type="button" variant="outline" size="sm" disabled={disabled || !inputDirectory.trim()} onClick={() => { setActionError(undefined); setSavedMessage(false); scan.mutate(); }}>
					{scan.isPending ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t(scan.isPending ? "settings.harness.executable.scanning" : "settings.harness.executable.scan")}
				</Button>
			</div>
			{scan.data ? <fieldset className="flex min-w-0 flex-col gap-2" disabled={disabled}>
				<legend className="mb-2 text-xs text-settings-muted">{t("settings.harness.executable.candidates")}</legend>
				{scan.data.candidates.length === 0 ? <p role="status" className="text-xs text-settings-muted">{t("settings.harness.executable.noCandidates")}</p> : scan.data.candidates.map((candidate) => <label key={candidate.path} className="flex min-w-0 items-start gap-2 rounded-md border border-border p-2 text-xs">
					<input type="radio" name={directoryId} value={candidate.path} checked={selectedPath === candidate.path} disabled={Boolean(candidate.error)} onChange={() => setSelectedPath(candidate.path)} className="mt-0.5 accent-current" />
					<span className="min-w-0"><span className="block break-all font-mono text-settings-label">{candidate.path}</span>
						<span className="text-settings-muted">{t(candidate.source === "desktop" ? "settings.harness.executable.desktop" : "settings.harness.executable.directorySource")}{candidate.version ? ` · ${candidate.version}` : ""}</span>
						{candidate.error ? <span className="mt-1 block break-words text-error">{candidate.error}</span> : null}
					</span>
				</label>)}
				{scan.data.truncated ? <p className="text-xs text-settings-muted">{t("settings.harness.executable.truncated")}</p> : null}
			</fieldset> : null}
			<div className="flex flex-wrap gap-2">
				<Button type="button" size="sm" disabled={disabled || !scan.data || !selectedPath} onClick={() => void save(false)}>{t("settings.harness.executable.apply")}</Button>
				<Button type="button" variant="ghost" size="sm" disabled={disabled || !saved?.executablePath} onClick={() => void save(true)}>{t("settings.harness.executable.restore")}</Button>
			</div>
			{error ? <p role="alert" className="break-words text-xs text-error">{error}</p> : null}
			{savedMessage ? <p role="status" className="text-xs text-settings-muted">{t("settings.harness.executable.saved")}</p> : null}
		</div>
		{hostId && remoteBrowse ? <RemoteDirectoryPicker hostId={hostId} hostLabel={labelForHost(hostId) ?? hostId} connected initialPath={inputDirectory} onChoose={(path) => { changeDirectory(path); setRemoteBrowse(false); }} onClose={() => setRemoteBrowse(false)} /> : null}
	</HarnessDetailGroup>;
}
