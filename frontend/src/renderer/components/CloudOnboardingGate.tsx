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

// Mounted once at the app root. Clears cached cloud data on sign-out and renders
// the local sign-in dialog. Signing in never prompts for a harness login: that
// lives only on the Harness settings page.
export function CloudOnboardingGate() {
	const { cloudEnabled } = useCloudGate();
	const { status } = useCloudSession();
	const queryClient = useQueryClient();

	// Sign-out must also drop the cached cloud data: the queries merely become
	// disabled, and their stale results would otherwise keep cloud projects on
	// the board (or flash another account's data on the next sign-in).
	useEffect(() => {
		if (status !== "unauthenticated") return;
		queryClient.removeQueries({ queryKey: cloudProjectsQueryKey });
		queryClient.removeQueries({ queryKey: cloudSessionsQueryKey });
		queryClient.removeQueries({ queryKey: cloudOrgQueryKey });
		queryClient.removeQueries({ queryKey: cloudMeQueryKey });
		queryClient.removeQueries({ queryKey: cloudBillingQueryKey });
		queryClient.removeQueries({ queryKey: providerConnectionsQueryKey });
		// A chat's events (kept an hour after it closes) and what they hold of
		// its history: the next account must read its own, which the control
		// plane may serve differently (a guest gets Setup output stripped).
		clearCloudChatCaches(queryClient);
	}, [status, queryClient]);

	if (!cloudEnabled) return null;
	return (
		<>
			<CloudLocalSignInDialog />
		</>
	);
}
