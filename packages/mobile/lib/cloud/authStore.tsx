import type { CloudClient } from "@aoagents/cloud-client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { AppState } from "react-native";
import type { Account } from "../account";
import { cancelAccountHostSync } from "../accountHosts";
import { createMobileCloudClient } from "./client";
import { CLOUD_BASE_URL, CLOUD_CONFIGURATION_ERROR, CLOUD_DEPLOYMENT } from "./config";
import { resolveCloudDeployment } from "./deployment";
import { resolveCloudWorkspace } from "./org";
import type { ResolvedMobileCloudOffering } from "./offering";
import { getAccountSessionOwner, signInToDeployment, type AccountSessionOwner } from "./sessionOwner";
import { registerWithLocalAuth, signInWithLocalAuth, type RegisterLocalAuthInput } from "./signIn";
import { wrapUnauthorized } from "./unauthorized";

export type CloudAuthState = {
	signedIn: boolean | null; account: Account | null;
	accountId: string | null; offering: ResolvedMobileCloudOffering | null;
	orgId: string | null; orgLoading: boolean; orgError: string | null;
	retryOrgResolution(): Promise<void>;
	sessionEpoch: number; client: CloudClient; baseUrl: string;
	isCurrentSession(): boolean;
	error: string | null; busy: boolean;
	signInWithWorkOS(): Promise<boolean>;
	signInLocal(email: string, password: string): Promise<void>;
	registerLocal(input: RegisterLocalAuthInput): Promise<void>;
	signOut(): Promise<void>;
};
const CloudAuthContext = createContext<CloudAuthState | null>(null);
export function useCloudAuth(): CloudAuthState {
	const ctx = useContext(CloudAuthContext);
	if (!ctx) throw new Error("useCloudAuth must be used within <CloudAuthProvider>");
	return ctx;
}

export function CloudAuthProvider({ children, baseUrl = CLOUD_BASE_URL, owner: injectedOwner }: {
	children: ReactNode; baseUrl?: string; owner?: AccountSessionOwner;
}) {
	const deployment = useMemo(() => baseUrl === CLOUD_BASE_URL ? CLOUD_DEPLOYMENT :
		resolveCloudDeployment({ cloudUrl: baseUrl, development: true }), [baseUrl]);
	const owner = useMemo(() => injectedOwner ?? getAccountSessionOwner(deployment), [injectedOwner, deployment]);
	const snapshot = useSyncExternalStore(owner.subscribe, owner.getSnapshot, owner.getSnapshot);
	const epoch = snapshot.epoch;
	const isCurrentSession = useCallback(() => owner.getSnapshot().epoch === epoch, [owner, epoch]);
	const [workspace, setWorkspace] = useState<{ epoch: number; orgId: string | null; offering?: ResolvedMobileCloudOffering; loading: boolean; error: string | null }>({ epoch, orgId: null, loading: false, error: null });
	const resolution = useRef<{ epoch: number; promise: Promise<void> } | null>(null);
	const signOut = useCallback(async () => {
		cancelAccountHostSync();
		await owner.signOut();
	}, [owner]);
	const boundClient = useCallback((capturedEpoch: number) => wrapUnauthorized(createMobileCloudClient({
		baseUrl, tokens: owner, isCurrent: () => capturedEpoch === owner.getSnapshot().epoch,
	}), (failedEpoch) => {
		if (failedEpoch === owner.getSnapshot().epoch) void signOut();
	}, () => capturedEpoch), [baseUrl, owner, signOut]);
	const client = useMemo(() => boundClient(epoch), [boundClient, epoch]);
	const resolveSession = useCallback((capturedEpoch: number): Promise<void> => {
		if (capturedEpoch !== owner.getSnapshot().epoch || owner.getSnapshot().busy) return Promise.resolve();
		if (resolution.current?.epoch === capturedEpoch) return resolution.current.promise;
		setWorkspace({ epoch: capturedEpoch, orgId: null, loading: true, error: null });
		let promise!: Promise<void>;
		promise = (async () => {
			try {
				if (CLOUD_CONFIGURATION_ERROR && deployment.key === CLOUD_DEPLOYMENT.key) throw new Error(CLOUD_CONFIGURATION_ERROR);
				await owner.initialize();
				if (capturedEpoch !== owner.getSnapshot().epoch) return;
				if (owner.getSnapshot().signedIn !== true) return;
				if (!owner.getSnapshot().account) throw new Error(owner.getSnapshot().error ?? "Could not verify your Cloud account.");
				const result = await resolveCloudWorkspace(boundClient(capturedEpoch), () => capturedEpoch === owner.getSnapshot().epoch);
				if (capturedEpoch === owner.getSnapshot().epoch) setWorkspace({ epoch: capturedEpoch, orgId: result.organization.id, offering: result.offering, loading: false, error: null });
			} catch (error) {
				if (capturedEpoch === owner.getSnapshot().epoch) setWorkspace({ epoch: capturedEpoch, orgId: null, loading: false, error: error instanceof Error ? error.message : "Could not load your cloud workspace." });
			} finally {
				if (capturedEpoch === owner.getSnapshot().epoch) setWorkspace((value) => value.epoch === capturedEpoch ? { ...value, loading: false } : value);
				if (resolution.current?.promise === promise) resolution.current = null;
			}
		})();
		resolution.current = { epoch: capturedEpoch, promise };
		return promise;
	}, [owner, boundClient, deployment]);
	const retryOrgResolution = useCallback(() => resolveSession(owner.getSnapshot().epoch), [owner, resolveSession]);
	useEffect(() => { void resolveSession(epoch); }, [epoch, snapshot.busy, resolveSession]);
	const orgId = workspace.epoch === epoch && snapshot.signedIn === true && !snapshot.busy ? workspace.orgId : null;
	useEffect(() => {
		const listener = AppState.addEventListener("change", (state) => {
			if (state === "active" && snapshot.signedIn && !orgId) void retryOrgResolution();
		});
		return () => listener.remove();
	}, [snapshot.signedIn, orgId, retryOrgResolution]);
	const signInWithWorkOS = useCallback(async () => {
		const accepted = await signInToDeployment(deployment, owner);
		if (accepted) await resolveSession(owner.getSnapshot().epoch);
		return accepted;
	}, [deployment, owner, resolveSession]);
	const signInLocal = useCallback(async (email: string, password: string) => {
		if (await owner.signIn(() => signInWithLocalAuth(baseUrl, email, password))) await resolveSession(owner.getSnapshot().epoch);
	}, [owner, baseUrl, resolveSession]);
	const registerLocal = useCallback(async (input: RegisterLocalAuthInput) => {
		if (await owner.signIn(() => registerWithLocalAuth(baseUrl, input))) await resolveSession(owner.getSnapshot().epoch);
	}, [owner, baseUrl, resolveSession]);
	const value: CloudAuthState = {
		signedIn: snapshot.signedIn, account: snapshot.account, sessionEpoch: epoch,
		accountId: snapshot.account?.id ?? null, offering: orgId ? workspace.offering ?? null : null,
		orgId, orgLoading: workspace.epoch === epoch && workspace.loading,
		orgError: workspace.epoch === epoch ? workspace.error : null, retryOrgResolution,
		client, baseUrl, error: CLOUD_CONFIGURATION_ERROR ?? snapshot.error, busy: snapshot.busy,
		signInWithWorkOS, signInLocal, registerLocal, signOut,
		isCurrentSession,
	};
	return <CloudAuthContext.Provider value={value}>{children}</CloudAuthContext.Provider>;
}
