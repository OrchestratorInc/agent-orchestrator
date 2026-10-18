// Shared React Query key for a single project's read-model (GET
// /api/v1/projects/{id}). Centralized so every consumer that reads, writes, or
// invalidates a project stays on the same cache entry — a PUT from one
// component (e.g. PromptOverrideDialog) correctly refetches the others (e.g.
// ProjectSettingsForm). Without this, two components independently writing
// `["project", id]` would silently desync if either changed the shape.
export const projectQueryKey = (id: string) => ["project", id] as const;
