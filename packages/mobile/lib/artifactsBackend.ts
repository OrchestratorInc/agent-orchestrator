import {
	artifactFileUrl,
	artifactImageSource,
	artifactMarkdownImageSource,
	deleteArtifact,
	getArtifactPage,
	getArtifactText,
	getArtifactVersion,
	getSession,
	listArtifactVersions,
	restoreArtifactVersion,
	syncArtifactVersions,
} from "./api";
import type { ArtifactAssetLoader } from "./artifactPageInliner";
import type { SessionArtifact } from "./artifacts";
import type { ArtifactVersion, ArtifactVersionSummary } from "./artifactVersions";
import { authHeaders, type ServerConfig } from "./config";
import { artifactAssetLoader } from "./useInlinedArtifactPage";

export type ArtifactSource = { uri: string; headers?: Record<string, string> };

/**
 * Where a session's artifacts are read, viewed and changed: the paired
 * machine's daemon, or Orchestrator Cloud for a cloud session
 * (cloud/artifacts.ts). The screens draw the same list, viewer and history
 * from either; what one cannot do is left out here, and so on screen.
 */
export type ArtifactsBackend = {
	/** For caches: names the machine, or the Cloud account. */
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
		key: `${config.host}:${config.httpPort}`,
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
		markdownImageSource: (sessionId, documentPath, url, versionOf) => artifactMarkdownImageSource(config, sessionId, documentPath, url, versionOf),
		assetLoader: (sessionId) => artifactAssetLoader(config, sessionId),
		remove: async (sessionId, path) => { await deleteArtifact(config, sessionId, path); },
		versions: (sessionId, path, signal) => listArtifactVersions(config, sessionId, path, { signal }),
		syncVersions: (sessionId, path, signal) => syncArtifactVersions(config, sessionId, path, { signal }),
		version: (sessionId, path, version, signal) => getArtifactVersion(config, sessionId, path, version, { signal }),
		restore: (sessionId, path, version) => restoreArtifactVersion(config, sessionId, path, version),
	};
}
