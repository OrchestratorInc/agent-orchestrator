import { delegateTask, getProjects, getSessions, killSession } from "../api";
import {
	cancelQueuedConversationTurn,
	getConversationPage,
	sendConversationMessage,
} from "../chat/api";
import { subscribeConversationEvents } from "../chat/conversationEvents";
import { machineIdentity, type ServerConfig } from "../config";
import { getWorkspaceManifest, getWorkspaceFile, getWorkspaceTree, searchWorkspaceFiles, sendFileFeedback, workspaceImageSource } from "../files/api";
import type { SessionSource } from "./types";
import { createLocalPullRequestSource } from "../pr/local";

/**
 * The paired-daemon environment, expressed as a SessionSource.
 *
 * A pure adapter: every method forwards to the function that already
 * implements it, with the active ServerConfig bound. No daemon behavior
 * changes here — this exists so screens can stop importing daemon functions
 * directly.
 */
export function createLocalSessionSource(cfg: ServerConfig): SessionSource {
	return {
		kind: "local",
		pullRequests: createLocalPullRequestSource(cfg),
		workspace: {
			scopeKey: JSON.stringify(["local", machineIdentity(cfg)]),
			isCurrent: () => true,
			getManifest: (id, options) => getWorkspaceManifest(cfg, id, options),
			getFile: (id, path, section, options) => getWorkspaceFile(cfg, id, path, section, options),
			getTree: (id, path, options) => getWorkspaceTree(cfg, id, path, options),
			search: (id, query, options) => searchWorkspaceFiles(cfg, id, query, options),
			imageSource: (id, path, side, version) => workspaceImageSource(cfg, id, path, side, version),
			sendFeedback: (id, comments, onSent) => sendFileFeedback(cfg, id, comments, onSent),
		},
		listProjects: () => getProjects(cfg),
		listSessions: async () => (await getSessions(cfg)).sessions,
		createSession: async (options) => {
			if (!options.projectId) throw new Error("Pick a project first");
			const session = await delegateTask(cfg, {
				projectId: options.projectId,
				brief: options.prompt ?? "",
				agent: options.harness,
				model: options.model,
				mode: options.mode ?? "chat",
				attachments: options.attachments,
			});
			return { id: session.id };
		},
		deleteSession: (id) => killSession(cfg, id),
		getConversationPage: (id, beforeSequence) => getConversationPage(cfg, id, beforeSequence),
		sendMessage: (id, input) => sendConversationMessage(cfg, id, input),
		cancelTurn: (id, turnId) => cancelQueuedConversationTurn(cfg, id, turnId),
		subscribeEvents: (id, listener) => subscribeConversationEvents(cfg, id, listener),
		// A local session never reports the cloud lifecycle stage that makes
		// `isResumable` true, so nothing in the UI should ever call this for a
		// local session. Throwing rather than silently no-opping surfaces a
		// wiring bug immediately instead of hiding it as a tap that does nothing.
		resumeSession: async () => {
			throw new Error("resumeSession is not supported for the local environment");
		},
	};
}
