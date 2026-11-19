import { useEffect, useMemo, useRef, useState } from "react";
import type { ServerConfig } from "../config";
import { haptics } from "../haptics";
import { checkGitHubAvailability, listGitHubOwners, type GitHubOwner, type GitHubRepositoryInput } from "./api";
import { isValidRemoteUrl, normalizeGitHubName } from "./importRules";

/** Where a project's origin comes from: a GitHub repository created for it, or a remote URL. */
export type RemoteMode = "github" | "url";

export type Availability =
	| { state: "idle" }
	| { state: "checking" }
	| { state: "available" }
	| { state: "unavailable"; message: string };

/**
 * The remote step the desktop asks for before importing a folder without an
 * origin: create a GitHub repository with gh on the machine (owner, name,
 * private or public), or give a remote URL. The owners and the name check
 * come from the machine, as the desktop's come from its own gh.
 */
export function useRemoteSetup({ config, hostId, active, suggestedName }: {
	config: ServerConfig | null;
	hostId: string | null;
	/** The form shows the step: only then are owners read and names checked. */
	active: boolean;
	/** The repository's name until one is typed: the folder's or the project's. */
	suggestedName: string;
}) {
	const [owners, setOwners] = useState<GitHubOwner[] | null>(null);
	const [mode, setMode] = useState<RemoteMode>("github");
	const [chosenOwner, setChosenOwner] = useState<string | null>(null);
	const [typedName, setTypedName] = useState<string | null>(null);
	const [isPrivate, setIsPrivate] = useState(true);
	const [url, setUrl] = useState("");
	const [availability, setAvailability] = useState<Availability>({ state: "idle" });

	// Owners belong to one machine's gh: another machine reads them again.
	const ownersFor = useRef<string | null>(null);
	useEffect(() => {
		if (!active || !config || !hostId || ownersFor.current === hostId) return;
		ownersFor.current = hostId;
		let cancelled = false;
		setOwners(null);
		setChosenOwner(null);
		listGitHubOwners(config)
			.catch(() => [] as GitHubOwner[])
			.then((found) => {
				if (cancelled) return;
				setOwners(found);
				// Without gh signed in there is nothing to create a repository with.
				if (!found.length) setMode("url");
			});
		return () => {
			cancelled = true;
			if (ownersFor.current === hostId) ownersFor.current = null;
		};
	}, [active, config, hostId]);

	const owner = chosenOwner ?? owners?.[0]?.login ?? "";
	const name = normalizeGitHubName(typedName ?? suggestedName);

	useEffect(() => {
		if (!active || mode !== "github" || !config || !owner || !name) {
			setAvailability({ state: "idle" });
			return;
		}
		setAvailability({ state: "checking" });
		const controller = new AbortController();
		const timer = setTimeout(() => {
			checkGitHubAvailability(config, owner, name, { signal: controller.signal })
				.then((result) => {
					if (controller.signal.aborted) return;
					setAvailability(result.available ? { state: "available" } : { state: "unavailable", message: result.message ?? "This name can't be used." });
				})
				.catch(() => {
					if (!controller.signal.aborted) setAvailability({ state: "unavailable", message: "Could not check this repository name." });
				});
		}, 300);
		return () => {
			controller.abort();
			clearTimeout(timer);
		};
	}, [active, config, mode, name, owner]);

	const urlTrimmed = url.trim();
	const ready = mode === "github"
		? Boolean(owner && name) && availability.state === "available"
		: isValidRemoteUrl(urlTrimmed);

	/** What prepare-git is sent, as the desktop sends it. */
	const request = useMemo((): { remoteUrl: string; githubRepository?: GitHubRepositoryInput } => {
		if (mode === "github") {
			return { remoteUrl: `https://github.com/${owner}/${name}.git`, githubRepository: { owner, name, private: isPrivate } };
		}
		return { remoteUrl: urlTrimmed };
	}, [isPrivate, mode, name, owner, urlTrimmed]);

	return {
		/** Null while the machine is asked; empty when gh there is signed out. */
		owners,
		mode,
		chooseMode: (next: RemoteMode) => {
			if (next === mode) return;
			haptics.select();
			setMode(next);
		},
		owner,
		chooseOwner: (login: string) => {
			haptics.select();
			setChosenOwner(login);
		},
		/** The field's text: what was typed, or the suggestion. */
		nameText: typedName ?? suggestedName,
		name,
		/** Typed differently than GitHub will store it: spaces become hyphens. */
		nameNormalized: typedName !== null && typedName.trim() !== name,
		setName: (next: string) => setTypedName(next),
		isPrivate,
		setPrivate: (next: boolean) => {
			haptics.select();
			setIsPrivate(next);
		},
		url,
		setUrl,
		urlInvalid: urlTrimmed.length > 0 && !isValidRemoteUrl(urlTrimmed),
		availability,
		ready,
		request,
	};
}

export type RemoteSetup = ReturnType<typeof useRemoteSetup>;
