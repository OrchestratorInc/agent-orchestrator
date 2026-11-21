import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { getAgentModels, refreshAgentModels, type AgentModelCatalog } from "../../lib/api";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { isCloudHost } from "../../lib/cloud/mapping";
import { agentModelCatalog } from "../../lib/cloud/modelCatalog";
import { cloudModelCatalog } from "../../lib/cloud/spawnSource";
import { ModelPickerSheet } from "../../lib/ModelPickerSheet";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { useApp } from "../../lib/store";
import { backOr } from "../../lib/backNavigation";
import { userFacingError } from "../../lib/connectionError";

export default function ModelSheetRoute() {
	const router = useRouter();
	const { config } = useApp();
	const cloud = useCloud();
	const { resultKey, agentId = "", projectId = "", selected = "", hostId } = useLocalSearchParams<{ resultKey?: string; agentId?: string; projectId?: string; selected?: string; hostId?: string }>();
	// Orchestrator Cloud's catalog for a cloud task, else the paired machine's.
	const cloudOrgId = isCloudHost(hostId) ? cloud.org?.id : undefined;
	const cloudClient = cloud.client;
	const [catalog, setCatalog] = useState<AgentModelCatalog>();
	const [error, setError] = useState<string>();
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);
	useEffect(() => {
		if (!agentId) return;
		if (isCloudHost(hostId) && !cloudOrgId) return;
		if (!isCloudHost(hostId) && !config) return;
		let cancelled = false;
		const polling = new AbortController();
		const load = cloudOrgId
			? agentModelCatalog(cloudClient, cloudOrgId, agentId).then((models) => cloudModelCatalog(agentId, models))
			: getAgentModels(config!, agentId, projectId, polling.signal);
		load.then((value) => { if (!cancelled) { setCatalog(value); setError(undefined); } })
			.catch((cause) => { if (!cancelled) setError(userFacingError(cause)); })
			.finally(() => { if (!cancelled) setLoading(false); });
		return () => { cancelled = true; polling.abort(); };
	}, [agentId, cloudClient, cloudOrgId, config, hostId, projectId]);
	const refresh = async () => {
		if (!agentId || refreshing) return;
		if (cloudOrgId) {
			setRefreshing(true);
			try {
				setCatalog(cloudModelCatalog(agentId, await cloudClient.listAgentModels(cloudOrgId, agentId)));
				setError(undefined);
			} catch (cause) {
				setError(userFacingError(cause));
			} finally {
				setRefreshing(false);
			}
			return;
		}
		if (!config || isCloudHost(hostId)) return;
		setRefreshing(true);
		try {
			setCatalog(await refreshAgentModels(config, agentId, projectId));
			setError(undefined);
		} catch (cause) {
			setError(userFacingError(cause));
		} finally {
			setRefreshing(false);
		}
	};
	return <ModelPickerSheet catalog={catalog} selected={selected} loading={loading} refreshing={refreshing} error={error} onClose={() => backOr(router)} onSelect={(value) => takeSheetResult<string>(resultKey)?.(value)} onRefresh={refresh} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
