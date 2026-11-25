import { useEffect } from "react";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { noteCloudSignInCompleted } from "../lib/cloud-telemetry";
import { clearCloudUser, identifyCloudUser } from "../lib/telemetry";

// Waits between attempts to get the daemon to acknowledge the current user (or
// the sign-out). The daemon is usually still starting when the session resolves;
// after the last attempt a signed-in launch stays anonymous rather than identify
// without a record. A sign-out repeats at the last interval until acknowledged.
export const BINDING_RETRY_MS = [1_000, 3_000, 10_000, 30_000, 60_000, 180_000];

// Hands the AO Cloud sign-in state to the daemon through main and notes it in the
// renderer once the daemon acknowledged it. Nothing identifies the user to
// PostHog: there is no identify(), alias() or person profile, and the email
// never leaves the app. The user ID becomes an event property only while
// "Include my account identity" is on. The daemon still records the binding so
// a sign-out or account switch rotates the anonymous install-scoped ID. Only the
// production WorkOS account counts; the dev-only local provider never does.
export function TelemetryIdentityRuntime() {
	const { session, status } = useCloudSession();
	const userId = session?.authProvider === "workos" ? session.user.id : null;
	// A completed sign-in is held by main until it is taken, once: here on the
	// session's notification, or on mounting when the sign-in link started the
	// app. The website visit id it may carry is never linked to anything.
	useEffect(() => {
		const cloud = aoBridge.cloud;
		if (!cloud) return;
		let mounted = true;
		const takeSignIn = () => {
			void cloud.takePendingSignIn().then((pending) => {
				if (!mounted || !pending) return;
				noteCloudSignInCompleted(pending.account);
			}, () => undefined);
		};
		const unsubscribe = cloud.onSessionChanged(takeSignIn);
		takeSignIn();
		return () => {
			mounted = false;
			unsubscribe();
		};
	}, []);
	useEffect(() => {
		// Until the session resolves, "no user" only means "not known yet"; telling
		// the daemon would be read as a sign-out and retire the install ID.
		if (status === "loading") return;
		if (!userId) clearCloudUser();
		let cancelled = false;
		void (async () => {
			// The same acknowledged, cancellable retry serves sign-in and sign-out: a
			// lost sign-out request would otherwise leave the live daemon exporting
			// under the account that just left.
			for (let attempt = 0; ; attempt++) {
				// Sign-in gives up after the schedule: staying anonymous is safe. A
				// sign-out never does: until the daemon acknowledges it, it keeps
				// exporting under the account that left, so it backs off to the last
				// interval and keeps going.
				if (userId && attempt > BINDING_RETRY_MS.length) return;
				const delay = attempt === 0 ? 0 : BINDING_RETRY_MS[Math.min(attempt, BINDING_RETRY_MS.length) - 1];
				if (delay) await new Promise((resolve) => setTimeout(resolve, delay));
				if (cancelled) return;
				const result = await Promise.resolve(aoBridge.telemetry.setCloudUser(userId)).catch(() => false);
				if (cancelled) return;
				if (result === "unsupported" && !userId) return;
				if (result === true) {
					if (userId) await identifyCloudUser({ id: userId });
					return;
				}
			}
		})();
		return () => {
			cancelled = true;
		};
	}, [userId, status]);
	return null;
}
