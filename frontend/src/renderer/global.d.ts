import type { AoBridge } from "../preload";

declare global {
	interface Window {
		ao?: AoBridge;
	}

	interface ImportMetaEnv {
		readonly VITE_AO_POSTHOG_KEY?: string;
		readonly VITE_AO_POSTHOG_HOST?: string;
		readonly VITE_AO_GIT_BRANCH?: string;
		readonly VITE_AO_GIT_COMMIT?: string;
		readonly VITE_AO_GIT_WORKTREE?: string;
		readonly VITE_AO_GIT_DIRTY?: string;
		/** "1" shows the artifact Share button before ARTIFACT_SHARING_ENABLED is on. */
		readonly VITE_AO_ARTIFACT_SHARES?: string;
	}
}

export {};
