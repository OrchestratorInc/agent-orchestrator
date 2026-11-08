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
		/** "on", "off" or "" (unset); see src/shared/teams-flag.ts. */
		readonly VITE_AO_TEAMS?: string;
	}
}

export {};
