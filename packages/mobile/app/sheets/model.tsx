import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { getAgentModels, refreshAgentModels, type AgentModelCatalog } from "../../lib/api";
import { ModelPickerSheet } from "../../lib/ModelPickerSheet";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { useApp } from "../../lib/store";
import { backOr } from "../../lib/backNavigation";
import { userFacingError } from "../../lib/connectionError";

export default function ModelSheetRoute() {
	const router = useRouter();
	const { config } = useApp();
	const { resultKey, agentId = "", projectId = "", selected = "" } = useLocalSearchParams<{ resultKey?: string; agentId?: string; projectId?: string; selected?: string }>();
	const [catalog, setCatalog] = useState<AgentModelCatalog>();
	const [error, setError] = useState<string>();
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);
	useEffect(() => { if (!config || !agentId) return; let cancelled = false; const polling = new AbortController(); getAgentModels(config, agentId, projectId, polling.signal).then((value) => { if (!cancelled) { setCatalog(value); setError(undefined); } }).catch((cause) => { if (!cancelled) setError(userFacingError(cause)); }).finally(() => { if (!cancelled) setLoading(false); }); return () => { cancelled = true; polling.abort(); }; }, [agentId, config, projectId]);
	const refresh = async () => {
		if (!config || !agentId || refreshing) return;
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
