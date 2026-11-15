import { useEffect } from "react";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { clearCloudUser, identifyCloudUser, noteCloudSignInWebVisitor } from "../lib/telemetry";

export const BINDING_RETRY_MS = [1_000, 3_000, 10_000, 30_000, 60_000, 180_000];

export function TelemetryIdentityRuntime() {
	const { session, status } = useCloudSession();
	const userId = session?.authProvider === "workos" ? session.user.id : null;
	const email = session?.authProvider === "workos" ? session.user.email : "";
	useEffect(() => {
		const cloud = aoBridge.cloud;
		if (!cloud) return;
		let mounted = true;
		const takeSignIn = () => {
			void cloud.takePendingSignIn().then((pending) => {
				if (!mounted || !pending) return;
				const { account, signIn } = pending;
				noteCloudSignInWebVisitor(account.authProvider === "workos" ? account.user.id : null, signIn.webDistinctId);
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
		if (status === "loading") return;
		if (!userId) clearCloudUser();
		let cancelled = false;
		void (async () => {
			for (let attempt = 0; ; attempt++) {
				if (userId && attempt > BINDING_RETRY_MS.length) return;
				const delay = attempt === 0 ? 0 : BINDING_RETRY_MS[Math.min(attempt, BINDING_RETRY_MS.length) - 1];
				if (delay) await new Promise((resolve) => setTimeout(resolve, delay));
				if (cancelled) return;
				const result = await Promise.resolve(aoBridge.telemetry.setCloudUser(userId)).catch(() => false);
				if (cancelled) return;
				if (result === "unsupported" && !userId) return;
				if (result === true) {
					if (userId) await identifyCloudUser({ id: userId, email });
					return;
				}
			}
		})();
		return () => {
			cancelled = true;
		};
	}, [userId, email, status]);
	return null;
}
