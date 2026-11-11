import {
	artifactFileUrl,
	artifactImageSource,
	artifactMarkdownImageSource,
	artifactStreamSource,
	deleteArtifact,
	getArtifactHead,
	getArtifactPage,
	getArtifactSize,
	getArtifactText,
	getArtifactTextWithin,
	getArtifactVersion,
	getSession,
	listArtifactVersions,
	restoreArtifactVersion,
	syncArtifactVersions,
} from "./api";
import type { ArtifactAssetLoader } from "./artifactPageInliner";
import type { SessionArtifact } from "./artifacts";
import type { ArtifactVersion, ArtifactVersionSummary } from "./artifactVersions";
import { authHeaders, machineIdentity, type ServerConfig } from "./config";
import { artifactAssetLoader } from "./useInlinedArtifactPage";

export type ArtifactSource = { uri: string; headers?: Record<string, string> };

/**
 * Where a session's artifacts are read, viewed and changed: the paired
 * machine's daemon, or Orchestrator Cloud for a cloud session
 * (cloud/artifacts.ts). The screens draw the same list, viewer and history
 * from either; what one cannot do is left out here, and so on screen.
 */
export type ArtifactsBackend = {
	/** For caches: names the machine (its stable host id), or the Cloud account. */
	key: string;
	cloud: boolean;
	/** Whether this person may delete a file or restore a version (on Cloud, the session's creator). */
	canChange: boolean;
	list(sessionId: string, signal?: AbortSignal): Promise<SessionArtifact[]>;
	text(sessionId: string, path: string, signal?: AbortSignal): Promise<string>;
	/** An HTML artifact as a page to draw (a daemon writes AO's theme in; Cloud's is as written). */
	page(sessionId: string, path: string, signal?: AbortSignal): Promise<string>;
	/** Where an image file loads from; undefined for a path that cannot be requested. */
	imageSource(sessionId: string, path: string, version?: string): ArtifactSource | undefined;
	/** Where a web view loads a file by itself: a page, or media. */
	webSource(sessionId: string, path: string): ArtifactSource | undefined;
	/**
	 * Where a native player or a download reads the file: every request it makes
	 * (ranges included) carries what the source needs, unlike a web view's.
	 */
	streamSource(sessionId: string, path: string, version?: string): ArtifactSource | undefined;
	/** A file's size without reading it, for one the list does not give; undefined when it cannot be learned. */
	size?(sessionId: string, path: string, signal?: AbortSignal): Promise<number | undefined>;
	/** The start of a file, to tell text from binary without reading it whole. */
	head?(sessionId: string, path: string, bytes: number, signal?: AbortSignal): Promise<string>;
	/** A file's text only if it is at most `maxBytes`; larger rejects with ArtifactTextTooLargeError. */
	textWithin?(sessionId: string, path: string, maxBytes: number, signal?: AbortSignal): Promise<string>;
	/** What an image a markdown artifact names loads from. */
	markdownImageSource(sessionId: string, documentPath: string, url: string, versionOf?: (path: string) => string | undefined): ArtifactSource | undefined;
	/** Reads the files a page links, to draw it whole. */
	assetLoader(sessionId: string): ArtifactAssetLoader;
	remove(sessionId: string, path: string): Promise<void>;
	versions(sessionId: string, path: string, signal?: AbortSignal): Promise<ArtifactVersionSummary[]>;
	/** Records an edit the daemon has not picked up; Cloud keeps versions by itself. */
	syncVersions?(sessionId: string, path: string, signal?: AbortSignal): Promise<ArtifactVersionSummary[]>;
	version(sessionId: string, path: string, version: number, signal?: AbortSignal): Promise<ArtifactVersion>;
	restore(sessionId: string, path: string, version: number): Promise<void>;
	/** Asks for fresh access after a load was refused for an expired one (Cloud). */
	renew?(): void;
};

export function daemonArtifactsBackend(config: ServerConfig): ArtifactsBackend {
	return {
		// The host id once known, so two machines that once shared an address never share a cache.
		key: machineIdentity(config),
		cloud: false,
		canChange: true,
		list: async (sessionId) => (await getSession(config, sessionId)).artifacts ?? [],
		text: (sessionId, path, signal) => getArtifactText(config, sessionId, path, { signal }),
		page: (sessionId, path, signal) => getArtifactPage(config, sessionId, path, { signal }),
		imageSource: (sessionId, path, version) => artifactImageSource(config, sessionId, path, version),
		webSource: (sessionId, path) => {
			const uri = artifactFileUrl(config, sessionId, path);
			return uri ? { uri, headers: authHeaders(config) } : undefined;
		},
		streamSource: (sessionId, path, version) => artifactStreamSource(config, sessionId, path, version),
		size: (sessionId, path, signal) => getArtifactSize(config, sessionId, path, { signal }),
		head: (sessionId, path, bytes, signal) => getArtifactHead(config, sessionId, path, bytes, { signal }),
		textWithin: (sessionId, path, maxBytes, signal) => getArtifactTextWithin(config, sessionId, path, maxBytes, { signal }),
		markdownImageSource: (sessionId, documentPath, url, versionOf) => artifactMarkdownImageSource(config, sessionId, documentPath, url, versionOf),
		assetLoader: (sessionId) => artifactAssetLoader(config, sessionId),
		remove: async (sessionId, path) => { await deleteArtifact(config, sessionId, path); },
		versions: (sessionId, path, signal) => listArtifactVersions(config, sessionId, path, { signal }),
		syncVersions: (sessionId, path, signal) => syncArtifactVersions(config, sessionId, path, { signal }),
		version: (sessionId, path, version, signal) => getArtifactVersion(config, sessionId, path, version, { signal }),
		restore: (sessionId, path, version) => restoreArtifactVersion(config, sessionId, path, version),
	};
}
