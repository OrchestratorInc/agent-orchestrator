export const DEV_BUILD_INFO = {
	branch: import.meta.env.VITE_AO_GIT_BRANCH?.trim() || "main",
	commit: import.meta.env.VITE_AO_GIT_COMMIT?.trim() || "unknown",
	worktree: import.meta.env.VITE_AO_GIT_WORKTREE?.trim() || "unknown",
	isDirty: import.meta.env.VITE_AO_GIT_DIRTY === "true",
} as const;
