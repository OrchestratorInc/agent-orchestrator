import { useMemo } from "react";
import type { CloudCpClient } from "../lib/cloud-cp";
import { createRendererCloudCpClient } from "../lib/cloud-cp/renderer-client";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudGate } from "./useCloudGate";
import { useSettings } from "./useSettings";

export interface UseCloudCpResult {
	client: CloudCpClient;
	ready: boolean;
	baseUrl: string;
	userId: string;
}

export { cloudCpFetch, createRendererCloudCpClient } from "../lib/cloud-cp/renderer-client";

export function useCloudCp(enabled = true): UseCloudCpResult {
	const { settings } = useSettings(undefined, enabled);
	const { cloudEnabled } = useCloudGate(enabled);
	const { session, status } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	const client = useMemo(() => createRendererCloudCpClient(baseUrl), [baseUrl]);
	return {
		client,
		ready: cloudEnabled && status === "authenticated" && baseUrl !== "",
		baseUrl,
		userId: session?.user.id ?? "",
	};
}
