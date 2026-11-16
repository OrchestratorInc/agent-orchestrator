import * as Crypto from "expo-crypto";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { haptics } from "../haptics";
import { connectedAgents, defaultCloudAgent, type CloudAgent } from "./agents";
import { collectPages } from "./client";
import { useCloud } from "./CloudProvider";
import { CloudError, cloudErrorMessage } from "./errors";
import { installGitHubApp } from "./githubInstall";
import {
	canCreateProject,
	hasActiveInstallation,
	matchRepositories,
	newProjectRequest,
	reachableRepositories,
	type ProjectSource,
} from "./newProject";
import type { CloudGitHubInstallation, CloudGitHubRepository } from "./types";

/**
 * The New cloud project sheet's state and actions, shared by its iOS and
 * Android faces. It creates a project the way the desktop does
 * (CreateProjectFlow): from a repository the Orchestrator GitHub App can reach,
 * or with no repository at all. The control plane owns the GitHub App; the
 * phone only opens its install page and waits for the installation to appear.
 */
export function useNewCloudProject({ onCreated }: {
	/** The project made, or the one that already holds the repository: open it. */
	onCreated(projectId: string): void;
}) {
	const { status, client, org, projects, connections, refreshProjects } = useCloud();
	const orgId = org?.id;
	const [source, setSource] = useState<ProjectSource>("github");
	const [installations, setInstallations] = useState<CloudGitHubInstallation[] | null>(null);
	const [githubUnavailable, setGithubUnavailable] = useState(false);
	const [repos, setRepos] = useState<CloudGitHubRepository[] | null>(null);
	// The last load failed for a reason other than a missing GitHub App, so
	// the sheet offers to try again rather than to connect GitHub.
	const [reposFailed, setReposFailed] = useState(false);
	const [query, setQuery] = useState("");
	const [repoId, setRepoId] = useState<string | null>(null);
	// Set by Change, so the search it brings back takes the keyboard at once.
	const [reselecting, setReselecting] = useState(false);
	const [name, setName] = useState("");
	const available = useMemo(() => connectedAgents(connections), [connections]);
	const [workerAgent, setWorkerAgent] = useState<CloudAgent | undefined>(() => defaultCloudAgent(connections, "claude-code"));
	const [orchestratorAgent, setOrchestratorAgent] = useState<CloudAgent | undefined>(() => defaultCloudAgent(connections));
	const [connecting, setConnecting] = useState(false);
	const [loadingRepos, setLoadingRepos] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	// One idempotency key per distinct request, so a retry after a dropped
	// response cannot make the project twice.
	const requestKey = useRef<{ payload: string; key: string } | null>(null);
	const mounted = useRef(true);
	const projectsRef = useRef(projects);
	projectsRef.current = projects;
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);

	// The connections arrive after the sheet opens, and one can go away while
	// it is open: keep each role on an agent that is connected, if any is.
	useEffect(() => {
		if (!workerAgent || !available.includes(workerAgent)) setWorkerAgent(defaultCloudAgent(connections, workerAgent ?? "claude-code"));
		if (!orchestratorAgent || !available.includes(orchestratorAgent)) setOrchestratorAgent(defaultCloudAgent(connections, orchestratorAgent));
	}, [available, connections, orchestratorAgent, workerAgent]);

	// The repositories belong to one organization: another one starts the
	// choice over, and only the latest read may answer.
	const reposRead = useRef(0);
	const shownOrg = useRef(orgId);
	useEffect(() => {
		if (shownOrg.current === orgId) return;
		shownOrg.current = orgId;
		reposRead.current++;
		requestKey.current = null;
		setInstallations(null);
		setRepos(null);
		setRepoId(null);
		setQuery("");
		setReposFailed(false);
		setError(null);
	}, [orgId]);

	const loadRepos = useCallback(async () => {
		if (!orgId) return;
		const read = ++reposRead.current;
		const current = () => mounted.current && read === reposRead.current;
		setLoadingRepos(true);
		try {
			const { installations: found } = await client.listGitHubInstallations(orgId);
			if (!current()) return;
			setInstallations(found);
			if (hasActiveInstallation(found)) {
				const items = await collectPages((cursor) => client.listGitHubRepositories(orgId, { cursor }));
				if (current()) setRepos(reachableRepositories(items));
			} else setRepos([]);
			if (current()) setReposFailed(false);
		} catch (cause) {
			if (!current()) return;
			// The GitHub App only exists on deployments that configure it.
			if (cause instanceof CloudError && cause.status === 404) {
				setGithubUnavailable(true);
				setSource("standalone");
			} else {
				setReposFailed(true);
				setError(cloudErrorMessage(cause));
			}
		} finally {
			if (current()) setLoadingRepos(false);
		}
	}, [client, orgId]);

	useEffect(() => { void loadRepos(); }, [loadRepos]);

	/** Open GitHub's install page, signed in, then wait for the new or updated installation. */
	const connectGitHub = async () => {
		if (!orgId || connecting) return;
		setConnecting(true);
		setError(null);
		try {
			await installGitHubApp(client, orgId, installations ?? [], () => mounted.current);
			if (mounted.current) await loadRepos();
		} catch (cause) {
			if (mounted.current) setError(cloudErrorMessage(cause));
		} finally {
			if (mounted.current) setConnecting(false);
		}
	};

	/** Ask GitHub again what the app can reach, for a repository added since the list loaded. */
	const refreshRepos = async () => {
		if (!orgId || !installations || loadingRepos) return;
		setLoadingRepos(true);
		await Promise.all(installations.filter((item) => item.status === "active").map((item) => client.syncGitHubInstallation(orgId, item.id).catch(() => {})));
		await loadRepos();
	};

	const matches = useMemo(() => matchRepositories(repos, query), [query, repos]);
	const repo = repos?.find((item) => item.githubRepositoryId === repoId);
	const canCreate = Boolean(orgId) && canCreateProject({ source, repoId, name });

	const chooseSource = (next: ProjectSource) => {
		if (next === source) return;
		haptics.select();
		setSource(next);
	};

	const chooseRepo = (id: string) => {
		haptics.select();
		setRepoId(id);
		setQuery("");
		setReselecting(false);
	};

	/** Back to searching; the choice goes until another is made. */
	const changeRepo = () => {
		haptics.select();
		setRepoId(null);
		setReselecting(true);
	};

	const chooseWorkerAgent = (agent: CloudAgent) => {
		haptics.select();
		setWorkerAgent(agent);
	};

	const chooseOrchestratorAgent = (agent: CloudAgent) => {
		haptics.select();
		setOrchestratorAgent(agent);
	};

	const create = async () => {
		if (!orgId || busy || !canCreate) return;
		const request = newProjectRequest({ source, repoId, name, workerAgent, orchestratorAgent });
		const payload = JSON.stringify(request);
		if (requestKey.current?.payload !== payload) requestKey.current = { payload, key: Crypto.randomUUID() };
		const key = requestKey.current.key;
		setBusy(true);
		setError(null);
		try {
			const { project } = request.source === "github"
				? await client.createGitHubProject(orgId, request.input, key)
				: await client.createStandaloneProject(orgId, request.input, key);
			requestKey.current = null;
			haptics.success();
			await refreshProjects();
			// Left while it was made: the project is in the list, and nothing
			// navigates from whatever screen is open now.
			if (mounted.current) onCreated(project.id);
		} catch (cause) {
			// The repository already has a project: open that one instead.
			if (cause instanceof CloudError && cause.code === "project_repository_exists" && request.source === "github") {
				const existing = projectsRef.current.find((item) => item.githubRepositoryId === request.input.githubRepositoryId);
				if (existing) {
					if (mounted.current) onCreated(existing.id);
					return;
				}
			}
			haptics.error();
			if (mounted.current) setError(cloudErrorMessage(cause));
		} finally {
			if (mounted.current) setBusy(false);
		}
	};

	return {
		status,
		org,
		source,
		chooseSource,
		/** This deployment has no GitHub App: only standalone projects. */
		githubUnavailable,
		hasInstallation: hasActiveInstallation(installations),
		/** Null until the first load settles. */
		repos,
		loadingRepos,
		/** The repositories could not be loaded; `retryRepos` asks again. */
		reposFailed,
		retryRepos: () => {
			setError(null);
			void loadRepos();
		},
		refreshRepos,
		connecting,
		connectGitHub,
		query,
		setQuery,
		/** Whether something is typed: the matches show in place of the choice. */
		searching: query.trim().length > 0,
		matches,
		repo,
		chooseRepo,
		changeRepo,
		reselecting,
		name,
		setName,
		/** The coding agents with a working credential; empty when none is connected. */
		available,
		workerAgent,
		chooseWorkerAgent,
		orchestratorAgent,
		chooseOrchestratorAgent,
		busy,
		error,
		dismissError: () => setError(null),
		canCreate,
		create,
	};
}

export type NewCloudProject = ReturnType<typeof useNewCloudProject>;
