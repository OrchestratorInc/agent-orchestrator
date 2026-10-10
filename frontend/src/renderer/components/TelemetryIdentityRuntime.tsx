import { useEffect } from "react";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { clearCloudUser, identifyCloudUser } from "../lib/telemetry";

// Ties the AO Cloud sign-in to the PostHog identity: identifies the user in the
// renderer (email as a person property, once) and hands the opaque user ID to the
// daemon through main so its events join the same person. Only the production
// WorkOS account is identified; the dev-only local provider never is.
export function TelemetryIdentityRuntime() {
	const { session } = useCloudSession();
	const userId = session?.authProvider === "workos" ? session.user.id : null;
	const email = session?.authProvider === "workos" ? session.user.email : "";
	useEffect(() => {
		void aoBridge.telemetry.setCloudUser(userId).catch(() => undefined);
		if (userId) void identifyCloudUser({ id: userId, email });
		else clearCloudUser();
	}, [userId, email]);
	return null;
}
