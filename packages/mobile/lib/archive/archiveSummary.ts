/** How many archived workers a machine has, in total and per project id ("" is standalone). */
export type ArchiveSummary = { total: number; byProject: Record<string, number> };

/** Whether two polls reported the same archive, so state can keep the old object. */
export function sameArchiveSummary(a: ArchiveSummary | null | undefined, b: ArchiveSummary | null | undefined): boolean {
	if (!a || !b) return (a ?? null) === (b ?? null);
	if (a.total !== b.total) return false;
	const keys = Object.keys(a.byProject);
	return keys.length === Object.keys(b.byProject).length && keys.every((key) => a.byProject[key] === b.byProject[key]);
}
