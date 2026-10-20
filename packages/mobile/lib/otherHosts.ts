import { useCallback, useEffect, useRef, useState } from "react";
import { reconcileRows } from "./rowSharing";
import { ApiError, getNotifications, getSessions, type DashboardSession, type DashboardStats, type OrchestratorLink, type ProjectInfo } from "./api";
import { isConfigured, type ServerConfig } from "./config";
import { connectToHost } from "./connectRuntime";
import { shouldKeepPolling, userFacingError } from "./connectionError";
import type { Endpoint } from "./endpoints";
import { findHost, type Host } from "./hosts";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";
import { pollIntervalFor } from "./pollInterval";
import { sameServerConfig } from "./sameConfig";
import { shouldRaceForUpgrade, UPGRADE_RACE_CHECK_MS } from "./upgradeRace";

export type HostSnapshot = {
	hostId: string;
	name: string;
	appearance: MachineAppearance;
	endpoints: Endpoint[];
	config: ServerConfig | null;
	connection: "closed" | "connecting" | "open";
	loading: boolean;
	error: string | null;
	errorStatus: number | null;
	projects: ProjectInfo[];
	projectsKnown: boolean;
	sessions: DashboardSession[];
	orchestrators: OrchestratorLink[];
	orchestratorId: string | null;
	stats: DashboardStats;
	notificationsUnread: number;
	lastSyncAt: number;
};

export type VerifiedHostSnapshot = HostSnapshot & {
	config: ServerConfig;
	connection: "open";
};

export function emptyHostSnapshot(host: Host): HostSnapshot {
	return {
		hostId: host.id,
		name: host.name,
		appearance: machineAppearance(host),
		endpoints: host.endpoints,
		config: null,
		connection: "connecting",
		loading: true,
		error: null,
		errorStatus: null,
		projects: [],
		projectsKnown: false,
		sessions: [],
		orchestrators: [],
		orchestratorId: null,
		stats: {},
		notificationsUnread: 0,
		lastSyncAt: 0,
	};
}

type Runner = { config: ServerConfig | null; refresh: () => Promise<void>; stop: () => void };
type Publish = (patch: Partial<HostSnapshot>) => void;
type RunnerSeed = { config: ServerConfig; connected: true };

export function verifiedHostSnapshot(hostId: string, snapshot: HostSnapshot | undefined): VerifiedHostSnapshot | null {
	if (snapshot?.hostId !== hostId || snapshot.connection !== "open" || snapshot.config?.hostId !== hostId) return null;
	return snapshot as VerifiedHostSnapshot;
}

/** One daemon's independent endpoint race and REST poll. */
function createOtherHostRunner(host: Host, previous: HostSnapshot | undefined, publish: Publish, seed: RunnerSeed | null): Runner {
	let stopped = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let upgradeTimer: ReturnType<typeof setInterval> | undefined;
	let upgrading = false;
	let connected = seed?.connected ?? false;
	let lastRaceAt = seed ? Date.now() : 0;
	let lastProjects = previous?.projects ?? [];
	let projectsKnown = previous?.projectsKnown ?? false;
	let busy: Promise<void> | null = null;
	const runner: Runner = {
		config: seed?.config ?? null,
		refresh: () => {
			if (busy) return busy;
			busy = (runner.config ? poll() : connect()).finally(() => { busy = null; });
			return busy;
		},
		stop: () => { stopped = true; if (timer) clearTimeout(timer); if (upgradeTimer) clearInterval(upgradeTimer); },
	};
	const update = (patch: Partial<HostSnapshot>) => { if (!stopped) publish(patch); };
	const schedule = (ms: number) => {
		if (timer) clearTimeout(timer);
		if (!stopped) timer = setTimeout(() => void runner.refresh(), ms);
	};
	const connect = async () => {
		if (stopped) return;
		// A saved address is not verified after a network change. A mounted chat
		// must not use its old bearer while the identity probe is still running.
		runner.config = null;
		connected = false;
		update({ config: null, connection: "connecting", loading: true });
		try {
			lastRaceAt = Date.now();
			const result = await connectToHost(host.id);
			if (stopped) return;
			if (!result.ok || result.hostId !== host.id) {
				runner.config = null;
				const incompatible = !result.ok && result.reason === "incompatible";
				update({ config: null, connection: "closed", loading: false, error: incompatible ? "Orchestrator versions are incompatible. Update Orchestrator on this phone and the machine." : "Host is unreachable", errorStatus: incompatible ? 426 : null });
				schedule(15_000);
				return;
			}
			runner.config = result.config;
			update({ config: result.config, connection: "connecting", endpoints: host.endpoints });
			await poll();
		} catch (cause) {
			if (stopped) return;
			runner.config = null;
			update({ config: null, connection: "closed", loading: false, error: userFacingError(cause), errorStatus: null });
			schedule(15_000);
		}
	};
	const checkUpgrade = async () => {
		const current = runner.config;
		if (stopped || upgrading || !connected || !current) return;
		upgrading = true;
		try {
			const known = (await findHost(host.id))?.endpoints ?? host.endpoints;
			if (stopped || runner.config !== current || !shouldRaceForUpgrade({
				currentKind: current.endpointKind,
				known,
				lastRaceAt,
				now: Date.now(),
				resumed: false,
			})) return;
			lastRaceAt = Date.now();
			const result = await connectToHost(host.id);
			if (stopped || !connected || runner.config !== current || !result.ok || result.hostId !== host.id) return;
			if (sameServerConfig(current, result.config)) return;
			runner.config = result.config;
			connected = false;
			update({ config: result.config, connection: "connecting", loading: true, endpoints: known });
			// An old-endpoint poll may still own `busy`; wait for it to finish
			// before starting the first poll on the verified upgrade path.
			if (busy) await busy;
			if (!stopped && runner.config === result.config) void runner.refresh();
		} catch {
			// A failed upgrade must leave the working endpoint alone.
		} finally {
			upgrading = false;
		}
	};
	upgradeTimer = setInterval(() => void checkUpgrade(), UPGRADE_RACE_CHECK_MS);
	const poll = async () => {
		const c = runner.config;
		if (!c || !isConfigured(c) || stopped) return;
		try {
			const answer = await getSessions(c, "all");
			if (stopped || runner.config !== c) return;
			if (answer.projects !== null) {
				lastProjects = answer.projects;
				projectsKnown = true;
			}
			update({
				config: c,
				connection: "open",
				loading: false,
				error: null,
				errorStatus: null,
				projects: lastProjects,
				projectsKnown,
				sessions: answer.sessions,
				orchestrators: answer.orchestrators,
				orchestratorId: answer.orchestratorId,
				stats: answer.stats,
				lastSyncAt: Date.now(),
			});
			connected = true;
			if (stopped || runner.config !== c) return;
			try {
				const page = await getNotifications(c, { status: "unread", limit: 1 });
				if (!stopped && runner.config === c) update({ notificationsUnread: page.unreadCount });
			} catch {
				// Older daemons may not serve notifications; the board remains usable.
			}
			if (!stopped && runner.config === c) schedule(pollIntervalFor(c));
		} catch (cause) {
			if (stopped || runner.config !== c) return;
			const status = cause instanceof ApiError ? cause.status : undefined;
			connected = false;
			update({ connection: "closed", loading: false, error: userFacingError(cause), errorStatus: status ?? null });
			if (!shouldKeepPolling(status)) {
				runner.config = null;
				update({ config: null }); // Do not leave a rejected bearer available to actions.
				return;
			}
			if (status === undefined || status === 421) {
				runner.config = null;
				update({ config: null }); // An old address is no longer verified for actions.
				schedule(2_000); // Race LAN/tunnel again after a network change.
			} else schedule(pollIntervalFor(c));
		}
	};
	return runner;
}

export function startOtherHost(host: Host, previous: HostSnapshot | undefined, publish: Publish): Runner {
	return createOtherHostRunner(host, previous, publish, null);
}

export function startVerifiedOtherHost(host: Host, snapshot: HostSnapshot, publish: Publish): Runner | null {
	const verified = verifiedHostSnapshot(host.id, snapshot);
	if (!verified) return null;
	return createOtherHostRunner(host, verified, publish, { config: verified.config, connected: true });
}

/** The selected host keeps its existing store/poll. These runners own every other paired host. */
export function useOtherHosts(hosts: Host[], selectedHostId: string | null, appActive: boolean) {
	const [snapshots, setSnapshots] = useState<Record<string, HostSnapshot>>({});
	const snapshotsRef = useRef<Record<string, HostSnapshot>>({});
	const runners = useRef(new Map<string, { generation: number; runner: Runner; host: Host }>());
	const generations = useRef(new Map<string, number>());
	const hostsRef = useRef(hosts);
	const appActiveRef = useRef(appActive);
	hostsRef.current = hosts;
	appActiveRef.current = appActive;

	const commitSnapshots = useCallback((update: (current: Record<string, HostSnapshot>) => Record<string, HostSnapshot>) => {
		const next = update(snapshotsRef.current);
		snapshotsRef.current = next;
		setSnapshots(next);
	}, []);

	const stopRunner = useCallback((hostId: string) => {
		generations.current.set(hostId, (generations.current.get(hostId) ?? 0) + 1);
		runners.current.get(hostId)?.runner.stop();
		runners.current.delete(hostId);
	}, []);

	const installRunner = useCallback((host: Host, build: (publish: Publish) => Runner | null): boolean => {
		stopRunner(host.id);
		const generation = (generations.current.get(host.id) ?? 0) + 1;
		generations.current.set(host.id, generation);
		let entry: { generation: number; runner: Runner; host: Host };
		const publish = (patch: Partial<HostSnapshot>) => {
			if (generations.current.get(host.id) !== generation || runners.current.get(host.id) !== entry) return;
			commitSnapshots((current) => {
				const previous = current[host.id] ?? emptyHostSnapshot(host);
				const next = { ...previous, ...patch };
				if (patch.sessions) next.sessions = reconcileRows(previous.sessions, patch.sessions);
				if (patch.projects) next.projects = reconcileRows(previous.projects, patch.projects);
				if (patch.orchestrators) next.orchestrators = reconcileRows(previous.orchestrators, patch.orchestrators);
				if (Object.keys(patch).every((key) => next[key as keyof HostSnapshot] === previous[key as keyof HostSnapshot])) return current;
				return { ...current, [host.id]: next };
			});
		};
		const runner = build(publish);
		if (!runner) return false;
		entry = { generation, runner, host };
		runners.current.set(host.id, entry);
		void runner.refresh();
		return true;
	}, [commitSnapshots, stopRunner]);

	const startColdRunner = useCallback((host: Host): boolean => {
		commitSnapshots((current) => ({
			...current,
			[host.id]: {
				...(current[host.id] ?? emptyHostSnapshot(host)),
				name: host.name,
				appearance: machineAppearance(host),
				endpoints: host.endpoints,
				config: null,
				connection: "connecting",
				loading: true,
			},
		}));
		return installRunner(host, (publish) => startOtherHost(host, snapshotsRef.current[host.id], publish));
	}, [commitSnapshots, installRunner]);

	const startSeededRunner = useCallback((host: Host, snapshot: VerifiedHostSnapshot): boolean => (
		installRunner(host, (publish) => startVerifiedOtherHost(host, snapshot, publish))
	), [installRunner]);

	useEffect(() => {
		const desiredBackgroundIds = new Set(
			appActive ? hosts.filter((host) => host.id !== selectedHostId).map((host) => host.id) : [],
		);
		for (const hostId of [...runners.current.keys()]) {
			if (!desiredBackgroundIds.has(hostId)) stopRunner(hostId);
		}

		if (!appActive) {
			commitSnapshots((current) => Object.fromEntries(hosts.map((host) => [
				host.id,
				{ ...(current[host.id] ?? emptyHostSnapshot(host)), name: host.name, appearance: machineAppearance(host), endpoints: host.endpoints, config: null, connection: "closed" as const, loading: false },
			])));
			return;
		}

		commitSnapshots((current) => Object.fromEntries(hosts.map((host) => [
			host.id,
			{ ...(current[host.id] ?? emptyHostSnapshot(host)), name: host.name, appearance: machineAppearance(host), endpoints: host.endpoints },
		])));
		for (const host of hosts) {
			const entry = runners.current.get(host.id);
			// Keep live connections for display-only edits, but never retain a
			// stale credential or route after an edit/account-directory refresh.
			if (entry && (entry.host.token !== host.token || JSON.stringify(entry.host.endpoints) !== JSON.stringify(host.endpoints))) {
				stopRunner(host.id);
			}
			if (desiredBackgroundIds.has(host.id) && !runners.current.has(host.id)) startColdRunner(host);
		}
	}, [hosts, selectedHostId, appActive, commitSnapshots, startColdRunner, stopRunner]);

	useEffect(() => {
		return () => {
			for (const hostId of [...runners.current.keys()]) stopRunner(hostId);
		};
	}, [stopRunner]);

	const refreshHost = useCallback(async (hostId: string) => {
		await runners.current.get(hostId)?.runner.refresh();
	}, []);
	const configForHost = useCallback((hostId: string) => runners.current.get(hostId)?.runner.config ?? null, []);
	const claimPrimary = useCallback((hostId: string): VerifiedHostSnapshot | null => {
		const entry = runners.current.get(hostId);
		const verified = verifiedHostSnapshot(hostId, snapshotsRef.current[hostId]);
		if (!entry || !verified || entry.runner.config !== verified.config) return null;
		stopRunner(hostId);
		return verified;
	}, [stopRunner]);
	const adoptPrimary = useCallback((snapshot: HostSnapshot): boolean => {
		const host = hostsRef.current.find((candidate) => candidate.id === snapshot.hostId);
		if (!host) return false;
		stopRunner(host.id);
		commitSnapshots((current) => ({
			...current,
			[host.id]: { ...snapshot, name: host.name, appearance: machineAppearance(host), endpoints: host.endpoints },
		}));
		const verified = verifiedHostSnapshot(host.id, snapshot);
		if (!verified || !appActiveRef.current) return false;
		return startSeededRunner(host, verified);
	}, [commitSnapshots, startSeededRunner, stopRunner]);
	return { snapshots, refreshHost, configForHost, claimPrimary, adoptPrimary };
}
