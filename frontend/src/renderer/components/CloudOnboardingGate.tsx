import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useCloudGate } from "../hooks/useCloudGate";
import { useCloudSession } from "../lib/cloud-session";
import { cloudOrgQueryKey } from "../hooks/useCloudOrg";
import { cloudBillingQueryKey } from "../hooks/useCloudOffering";
import { cloudMeQueryKey } from "../lib/cloud-offering";
import { cloudProjectsQueryKey, cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { providerConnectionsQueryKey } from "../hooks/useProviderConnections";
import { CloudLocalSignInDialog } from "./CloudLocalSignInDialog";
import { clearCloudChatCaches } from "../lib/cloud-chat-history";

export function CloudOnboardingGate() {
	const { cloudEnabled } = useCloudGate();
	const { status } = useCloudSession();
	const queryClient = useQueryClient();

	useEffect(() => {
		if (status !== "unauthenticated") return;
		queryClient.removeQueries({ queryKey: cloudProjectsQueryKey });
		queryClient.removeQueries({ queryKey: cloudSessionsQueryKey });
		queryClient.removeQueries({ queryKey: cloudOrgQueryKey });
		queryClient.removeQueries({ queryKey: cloudMeQueryKey });
		queryClient.removeQueries({ queryKey: cloudBillingQueryKey });
		queryClient.removeQueries({ queryKey: providerConnectionsQueryKey });
		clearCloudChatCaches(queryClient);
	}, [status, queryClient]);

	if (!cloudEnabled) return null;
	return (
		<>
			<CloudLocalSignInDialog />
		</>
	);
}
