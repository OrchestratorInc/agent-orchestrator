import * as Crypto from "expo-crypto";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ServerConfig } from "../config";
import { haptics } from "../haptics";
import { connectedSpawnHosts } from "../hostRoute";
import { useApp } from "../store";
import {
	addFolderProject,
	cleanupClone,
	initializeFolder,
	listDirectories,
	prepareClone,
	prepareImportGit,
	validateImport,
	type DirectoryEntry,
	type DirectoryListing,
	type ImportAction,
	type ImportValidation,
	type GitHubRepositoryInput,
} from "./api";
import { importState, needsRemote, nextAction, preparationFailureMessage, suggestedGitHubName, type ImportState } from "./importRules";
import {
	canCreateLocalProject,
	createUnsupported,
	DEFAULT_CLONE_PARENT,
	filterDirectories,
	folderNameFor,
	joinPath,
	localProjectErrorMessage,
	localProjectRequest,
	UNSUPPORTED_NEW_PROJECT,
	type LocalProjectSource,
} from "./newLocalProject";
import { loadProjectPlaces, rememberProjectPlace, type ProjectPlaces } from "./recentFolders";
import { useGitHubRepositories } from "./useGitHubRepositories";
import { useRemoteSetup } from "./useRemoteSetup";

/** The steps a new, empty project goes through: the folder is made and committed, then given a remote. */
const NEW_PROJECT_ACTIONS: readonly ImportAction[] = ["git_init", "git_commit", "create_remote_repository"];

/**
 * A clone made but not registered yet, as the desktop keeps one until the
 * project is added, with the machine that made it: cleanup goes there.
 */
type PreparedClone = { path: string; preparationId: string; remoteUrl: string; destinationParent: string; hostId: string; config: ServerConfig };

/**
 * A GitHub repository this form's create already made on the machine whose
 * push then failed: the daemon keeps the remote step required and records the
 * repository, so resending the same request retries only the push. The name
 * is taken by then, by this very attempt, so it isn't checked again.
 */
type RemoteResume = { hostId: string; path: string; request: { remoteUrl: string; githubRepository: GitHubRepositoryInput } };

/** What the daemon said about the folder being added, and for which path. */
type ImportCheck = { path: string; validation: ImportValidation; state: ImportState };

/** What the folder browser is choosing: the project's own folder, or where a clone or new project goes. */
export type BrowseTarget = "folder" | "destination";

/**
 * The New project sheet's Local face, shared by its iOS and Android forms. It
 * adds a project on one of the paired machines with the desktop's own steps
 * (CreateProjectFlow): an existing folder, browsed on the machine; a
 * repository cloned into a new folder; or a new, empty folder. Each folder is
 * validated the way the desktop validates it, the Git setup it needs (git
 * init, a first commit, a remote, created on GitHub or given as a URL) runs
 * one step at a time as the desktop runs it, and only then is it added. Each
 * machine remembers where its last projects went, and the browser starts there.
 */
export function useNewLocalProject({ onCreated }: {
	/** The project made on `hostId`: open it. */
	onCreated(hostId: string, projectId: string): void;
}) {
	const { hostStates, currentHostId, configForHost, refreshHost } = useApp();
	const machines = useMemo(() => connectedSpawnHosts(hostStates), [hostStates]);
	const [chosenHostId, setChosenHostId] = useState<string | null>(null);
	// The chosen machine while it stays connected; otherwise the default one, or the first.
	const hostId = machines.find((machine) => machine.id === chosenHostId)?.id
		?? machines.find((machine) => machine.id === currentHostId)?.id
		?? machines[0]?.id
		?? null;
	const machine = machines.find((item) => item.id === hostId);
	const config = hostId ? configForHost(hostId) : null;
	// The machine as committed, not as a render that React may still discard:
	// a clone finishing for the machine still shown must not look stale.
	const hostRef = useRef(hostId);
	useLayoutEffect(() => { hostRef.current = hostId; }, [hostId]);

	const [source, setSource] = useState<LocalProjectSource>("folder");
	const [folder, setFolder] = useState<string | null>(null);
	const folderRef = useRef(folder);
	useLayoutEffect(() => { folderRef.current = folder; }, [folder]);
	const [remoteUrl, setRemoteUrl] = useState("");
	const [destinationParent, setDestinationParent] = useState(DEFAULT_CLONE_PARENT);
	const [name, setNameValue] = useState("");

	// The browser: the folder it shows, the machine user's home (the first
	// listing), and a filter over a long listing.
	const [browseTarget, setBrowseTarget] = useState<BrowseTarget | null>("folder");
	const [listing, setListing] = useState<DirectoryListing | null>(null);
	const [home, setHome] = useState<string | undefined>(undefined);
	const [filter, setFilter] = useState("");
	const [listingBusy, setListingBusy] = useState(false);
	const [listingError, setListingError] = useState<string | null>(null);

	const [places, setPlaces] = useState<ProjectPlaces>({ recent: [] });
	const github = useGitHubRepositories(source === "clone");

	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	/** A new name no longer answers to the last failure. */
	const setName = (value: string) => {
		setNameValue(value);
		setError(null);
	};
	// The validation of the folder being added, the step running now, and a
	// clone or new folder already made on the machine but not added yet.
	const [check, setCheck] = useState<ImportCheck | null>(null);
	const [checking, setChecking] = useState(false);
	const [runningAction, setRunningAction] = useState<ImportAction | null>(null);
	const [prepared, setPrepared] = useState<PreparedClone | null>(null);
	const preparedRef = useRef<PreparedClone | null>(null);
	preparedRef.current = prepared;
	// One id per new folder, kept across retries, so a create whose response
	// was lost resumes the folder it made (the daemon records the id there).
	const createId = useRef<{ path: string; id: string } | null>(null);
	const [resume, setResume] = useState<RemoteResume | null>(null);

	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);

	// Only the latest listing answers; another machine starts it over.
	const listRead = useRef(0);
	/** `quiet` keeps the folder shown when the one asked for is gone: a remembered folder may have moved. */
	const browse = useCallback(async (path?: string, { quiet = false }: { quiet?: boolean } = {}) => {
		if (!config) return;
		const read = ++listRead.current;
		const current = () => mounted.current && read === listRead.current;
		setListingBusy(true);
		setListingError(null);
		try {
			const next = await listDirectories(config, path);
			if (!current()) return;
			setListing(next);
			setFilter("");
			if (!path) setHome(next.path);
		} catch (cause) {
			if (current() && !quiet) setListingError(localProjectErrorMessage(cause));
		} finally {
			if (current()) setListingBusy(false);
		}
	}, [config]);

	const shownHost = useRef<string | null>(null);
	useEffect(() => {
		if (!hostId || shownHost.current === hostId) return;
		shownHost.current = hostId;
		setListing(null);
		setHome(undefined);
		setFolder(null);
		setBrowseTarget(source === "folder" ? "folder" : null);
		setDestinationParent(DEFAULT_CLONE_PARENT);
		setPlaces({ recent: [] });
		setError(null);
		setCheck(null);
		createId.current = null;
		setResume(null);
		const shown = hostId;
		void (async () => {
			const remembered = await loadProjectPlaces(shown);
			if (!mounted.current || shownHost.current !== shown) return;
			setPlaces(remembered);
			if (remembered.destination) setDestinationParent(remembered.destination);
			// Home first, which names `~`; then the folder the last project sat in.
			await browse();
			if (remembered.recent[0] && shownHost.current === shown) await browse(remembered.recent[0], { quiet: true });
		})();
	}, [browse, hostId, source]);

	const chooseHost = (id: string) => {
		// A create in flight belongs to the machine it started on.
		if (id === hostId || busy) return;
		haptics.select();
		setChosenHostId(id);
	};

	const chooseSource = (next: LocalProjectSource) => {
		// The create in flight was asked for from this source; the form keeps showing it.
		if (next === source || busy) return;
		haptics.select();
		setSource(next);
		setError(null);
		// The folder browser opens by itself until a folder is chosen; where a
		// clone or new project goes is only browsed when Change asks for it.
		setBrowseTarget(next === "folder" && !folder ? "folder" : null);
	};

	/** Into a subfolder, or up to the parent. */
	const openDirectory = (path: string) => {
		haptics.select();
		void browse(path);
	};

	/** Validates a folder as the desktop does when one is chosen; only the latest check answers. */
	const checkRead = useRef(0);
	const runCheck = useCallback(async (path: string) => {
		if (!config) return;
		const read = ++checkRead.current;
		setChecking(true);
		setCheck(null);
		try {
			const validation = await validateImport(config, path);
			if (mounted.current && read === checkRead.current) setCheck({ path, validation, state: importState(validation) });
		} catch (cause) {
			if (mounted.current && read === checkRead.current) setError(localProjectErrorMessage(cause));
		} finally {
			if (mounted.current && read === checkRead.current) setChecking(false);
		}
	}, [config]);

	/** The browsed folder, or a repository row, becomes the choice. */
	const choose = (path: string) => {
		if (busy) return;
		haptics.select();
		if (browseTarget === "destination") {
			setDestinationParent(path);
		} else {
			setFolder(path);
			void runCheck(path);
		}
		setBrowseTarget(null);
		setError(null);
	};

	/** A repository row picks itself; any other folder opens. */
	const pressEntry = (entry: DirectoryEntry) => {
		if (browseTarget === "folder" && entry.gitRepo) choose(entry.path);
		else openDirectory(entry.path);
	};

	/** Back to browsing, starting where the current choice lives. */
	const changeFolder = (target: BrowseTarget) => {
		// A create in flight holds its folder and destination: moving the
		// destination would start removing the clone it is about to register.
		if (busy) return;
		haptics.select();
		setBrowseTarget(target);
		if (!listing && !listingBusy) void browse();
	};

	const cancelBrowse = () => {
		haptics.select();
		setBrowseTarget(null);
	};

	const entries = useMemo(() => filterDirectories(listing?.entries ?? [], filter), [filter, listing]);
	/** Shortcuts to where recent projects sat, other than the folder shown. */
	const recentPlaces = useMemo(() => filter.trim() ? [] : places.recent.filter((path) => path !== listing?.path), [filter, listing?.path, places.recent]);
	const atRoot = !listing || listing.parent === listing.path;
	/**
	 * Removes a prepared clone the user moved away from (another URL, another
	 * place, another machine), on the machine that made it, as the desktop
	 * removes it. From the moment removal starts the clone is retired: it is
	 * never registered, even if the user goes back to its URL, since its
	 * checkout may be half gone. It stays known until removal succeeds, so a
	 * failure is said and the next Create tries again first. Calls while a
	 * removal runs share it.
	 */
	const retired = useRef(new WeakSet<PreparedClone>());
	const removing = useRef(new WeakMap<PreparedClone, Promise<boolean>>());
	const cleanupPrepared = useCallback((clone: PreparedClone): Promise<boolean> => {
		retired.current.add(clone);
		const running = removing.current.get(clone);
		if (running) return running;
		const removal = (async () => {
			try {
				await cleanupClone(clone.config, { path: clone.path, preparationId: clone.preparationId });
			} catch {
				if (mounted.current) setError(`Orchestrator couldn't remove the unused clone at ${clone.path}. Create tries again.`);
				return false;
			} finally {
				removing.current.delete(clone);
			}
			if (preparedRef.current === clone) {
				preparedRef.current = null;
				if (mounted.current) {
					setPrepared(null);
					setCheck((current) => current?.path === clone.path ? null : current);
				}
			}
			return true;
		})();
		removing.current.set(clone, removal);
		return removal;
	}, []);
	const preparedMatches = (clone: PreparedClone) => !retired.current.has(clone) && clone.hostId === hostId
		&& clone.remoteUrl === remoteUrl.trim() && clone.destinationParent === destinationParent.trim();
	useEffect(() => {
		if (prepared && !retired.current.has(prepared) && !(prepared.hostId === hostId && prepared.remoteUrl === remoteUrl.trim() && prepared.destinationParent === destinationParent.trim())) {
			void cleanupPrepared(prepared);
		}
	}, [cleanupPrepared, destinationParent, hostId, prepared, remoteUrl]);
	// The sheet closed with a clone still unregistered: nothing is left to retry from, so it goes now.
	useEffect(() => () => {
		const clone = preparedRef.current;
		if (clone) void cleanupClone(clone.config, { path: clone.path, preparationId: clone.preparationId }).catch(() => {});
	}, []);

	// The step list and the remote the form shows: a folder's from its check, a
	// new project's always, a clone's only when its checkout needs one.
	const checkFor = source === "folder" ? (check && check.path === folder ? check : null)
		: source === "clone" ? (prepared && preparedMatches(prepared) && check?.path === prepared.path ? check : null)
		: null;
	const setupActions: ImportAction[] = source === "new"
		? [...NEW_PROJECT_ACTIONS]
		: checkFor?.state.kind === "setup" ? checkFor.state.actions : [];
	const blocked = checkFor?.state.kind === "blocked" ? checkFor.state.message : null;
	const remoteNeeded = needsRemote(setupActions);
	const remote = useRemoteSetup({
		config,
		hostId,
		active: remoteNeeded,
		suggestedName: source === "new" ? folderNameFor(name) : suggestedGitHubName(checkFor?.path ?? folder ?? ""),
	});

	// The folder Create sets up: the chosen one, the clone, or the new one.
	const targetPath = source === "folder" ? folder
		: source === "clone" ? (prepared && preparedMatches(prepared) ? prepared.path : null)
		: joinPath(destinationParent.trim(), folderNameFor(name));
	// The same repository this attempt made, on the same folder and machine:
	// ready without the name check (the name is taken, by it); anything else is
	// checked as any new repository is.
	const outgoing = remote.request;
	const resuming = Boolean(resume && remoteNeeded && resume.hostId === hostId && resume.path === targetPath
		&& outgoing.githubRepository
		&& resume.request.githubRepository.owner === outgoing.githubRepository.owner
		&& resume.request.githubRepository.name === outgoing.githubRepository.name
		&& resume.request.githubRepository.private === outgoing.githubRepository.private);

	// Not while a folder is being browsed: Create would use the one chosen before.
	const canCreate = Boolean(config) && !busy && !checking && !blocked && browseTarget === null
		&& canCreateLocalProject({ source, folder, remoteUrl, destinationParent, name })
		&& (source !== "folder" || Boolean(checkFor))
		&& (!remoteNeeded || remote.ready || resuming);

	const finish = async (createdOn: string, projectId: string, place: { folder?: string; destination?: string }) => {
		haptics.success();
		if (mounted.current) setResume(null);
		await rememberProjectPlace(createdOn, place).catch(() => {});
		await refreshHost(createdOn).catch(() => {});
		if (mounted.current) onCreated(createdOn, projectId);
	};

	/**
	 * The desktop's Git setup: one approved step at a time until the daemon
	 * says the folder can be added, stopping on a failed step or a folder that
	 * turns out blocked. Returns the last validation.
	 */
	const runSetup = async (cfg: NonNullable<typeof config>, start: ImportValidation, on: string, target: string): Promise<ImportValidation> => {
		let current = start;
		const sending = resuming && resume ? resume.request : outgoing;
		while (current.nextStep === "prepare_git") {
			const action = nextAction(current);
			if (!action) throw new Error("Orchestrator couldn't finish setting up Git for this folder.");
			if (mounted.current) setRunningAction(action);
			const { events, validation } = await prepareImportGit(cfg, {
				path: current.root.repoPath,
				approvedActions: current.root.requiredActions,
				...(needsRemote(current.root.requiredActions) ? sending : {}),
			});
			const failed = events.find((event) => event.state === "error");
			if (failed) {
				// The repository was made and the folder points at it, but the push
				// failed: the daemon still asks for the remote step. Keep this request
				// so the next Create resends it and retries the push.
				if (failed.action === "create_remote_repository" && sending.githubRepository
					&& validation.root.hasOrigin && validation.root.requiredActions.includes("create_remote_repository") && mounted.current) {
					// The path as this form named it, as targetPath does: the daemon's is
					// absolute (`~` expanded) and would never match `~/ao/projects/…`.
					setResume({ hostId: on, path: target, request: { remoteUrl: sending.remoteUrl, githubRepository: sending.githubRepository } });
				}
				throw new Error(preparationFailureMessage(failed));
			}
			const state = importState(validation);
			if (state.kind === "blocked") throw new Error(state.message);
			if (nextAction(validation) === action) throw new Error("Orchestrator couldn't finish setting up Git for this folder.");
			current = validation;
		}
		if (mounted.current) setRunningAction(null);
		return current;
	};

	/** Validates a folder that was just made or cloned, and reports why it can't be added. */
	const validated = async (cfg: NonNullable<typeof config>, path: string, on: string): Promise<ImportValidation> => {
		const validation = await validateImport(cfg, path);
		const state = importState(validation);
		// Only the machine still shown takes the answer.
		if (mounted.current && hostRef.current === on) setCheck({ path, validation, state });
		if (state.kind === "blocked") throw new Error(state.message);
		return validation;
	};

	const create = async () => {
		if (!config || !hostId || !canCreate) return;
		const request = localProjectRequest({ source, folder, remoteUrl, destinationParent, name });
		const createdOn = hostId;
		setBusy(true);
		setError(null);
		try {
			if (request.source === "clone") {
				let clone = preparedRef.current;
				// A clone left from another URL, place or machine, or one already being
				// removed, goes first (waiting for a removal that is running); then a fresh one.
				if (clone && !preparedMatches(clone)) {
					if (!(await cleanupPrepared(clone))) return;
					clone = null;
				}
				if (!clone) {
					const made = await prepareClone(config, { remoteUrl: request.input.remoteUrl, destinationParent: request.input.destinationParent });
					const fresh: PreparedClone = { ...made, remoteUrl: request.input.remoteUrl, destinationParent: request.input.destinationParent, hostId: createdOn, config };
					// The machine changed or the sheet closed while it cloned: the clone isn't this form's.
					if (!mounted.current || hostRef.current !== createdOn) {
						void cleanupClone(config, { path: made.path, preparationId: made.preparationId }).catch(() => {});
						return;
					}
					clone = fresh;
					setPrepared(clone);
					preparedRef.current = clone;
				}
				let validation: ImportValidation;
				try {
					validation = await validated(config, clone.path, createdOn);
				} catch (cause) {
					// A checkout that can't become a project isn't kept.
					await cleanupPrepared(clone);
					throw cause;
				}
				// The checkout needs a remote of its own: the form asks for it, and Create goes on.
				if (needsRemote(validation.root.requiredActions) && !remote.ready) return;
				await runSetup(config, validation, createdOn, clone.path);
				// Its removal started meanwhile: the checkout may be going, so it isn't registered.
				if (retired.current.has(clone)) throw new Error("The clone was removed before it could be added. Try again.");
				const id = await addFolderProject(config, { path: clone.path, clonePreparationId: clone.preparationId, ...(request.input.name ? { name: request.input.name } : {}) });
				preparedRef.current = null;
				if (mounted.current) setPrepared(null);
				await finish(createdOn, id, { destination: request.input.destinationParent });
				return;
			}
			if (request.source === "new") {
				// The same id on every try for this folder: the daemon resumes the folder
				// an earlier try made (a lost response, a failed setup step) and refuses
				// any other that is already there.
				if (createId.current?.path !== request.input.path) createId.current = { path: request.input.path, id: Crypto.randomUUID() };
				try {
					await initializeFolder(config, request.input.path, { create: true, createId: createId.current.id });
				} catch (cause) {
					if (createUnsupported(cause)) throw new Error(UNSUPPORTED_NEW_PROJECT);
					throw cause;
				}
				await runSetup(config, await validated(config, request.input.path, createdOn), createdOn, request.input.path);
				await finish(createdOn, await addFolderProject(config, request.input), { destination: destinationParent.trim() });
				return;
			}
			const validation = checkFor?.validation ?? await validated(config, request.input.path, createdOn);
			const done = await runSetup(config, validation, createdOn, request.input.path);
			await finish(createdOn, await addFolderProject(config, { ...request.input, path: done.root.repoPath }), { folder: request.input.path });
		} catch (cause) {
			haptics.error();
			if (mounted.current) {
				setError(localProjectErrorMessage(cause));
				// What the daemon thinks now, so the form shows the steps still to run;
				// only for the folder still chosen, never over a newer pick's check.
				if (source === "folder" && folder && folderRef.current === folder) void runCheck(folder);
			}
		} finally {
			if (mounted.current) {
				setBusy(false);
				setRunningAction(null);
			}
		}
	};

	return {
		machines,
		hostId,
		machine,
		chooseHost,
		source,
		chooseSource,
		folder,
		remoteUrl,
		setRemoteUrl,
		destinationParent,
		name,
		setName,
		browseTarget,
		listing,
		home,
		entries,
		recentPlaces,
		atRoot,
		filter,
		setFilter,
		listingBusy,
		listingError,
		retryListing: () => void browse(listing?.path),
		openDirectory,
		pressEntry,
		choose,
		changeFolder,
		cancelBrowse,
		/** The folder is being validated. */
		checking,
		/** Why the folder can't be added, in the desktop's words. */
		blocked,
		/** The Git setup Create will run, in order; empty when there is none. */
		setupActions,
		/** The step running now. */
		runningAction,
		/** The remote step, when the setup gives the folder an origin. */
		remote,
		remoteNeeded,
		/** Create retries the push to the repository this attempt already made. */
		remoteResuming: resuming,
		github,
		/** A GitHub repository's clone URL into the field. */
		takeRepository: (repo: Parameters<typeof github.take>[0]) => {
			if (busy) return;
			haptics.select();
			setRemoteUrl(github.take(repo));
			setError(null);
		},
		busy,
		error,
		dismissError: () => setError(null),
		canCreate,
		create,
	};
}

export type NewLocalProject = ReturnType<typeof useNewLocalProject>;
