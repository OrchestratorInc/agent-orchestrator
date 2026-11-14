// Shared by the visible review and its background prefetch. Paths are part of
// the query key, so different batch sizes cannot reuse the same request.
export const WORKSPACE_REVIEW_BATCH_SIZE = 24;
// Bound query observers, parsed patches and non-virtualized fallback rows too.
export const WORKSPACE_REVIEW_PAGE_SIZE = WORKSPACE_REVIEW_BATCH_SIZE * 4;
export const WORKSPACE_REVIEW_INITIAL_BATCHES = 1;
export const WORKSPACE_REVIEW_BATCH_WAVE = 2;
