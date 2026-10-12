/** Reconcile one machine's JSON poll at the boundary, not inside every row's render. */
export function reconcileRows<T extends { id: string }>(previous: readonly T[], incoming: T[]): T[] {
	const byId = new Map(previous.map((row) => [row.id, row]));
	const shared = incoming.map((row) => {
		const old = byId.get(row.id);
		return old && (old === row || JSON.stringify(old) === JSON.stringify(row)) ? old : row;
	});
	return shared.length === previous.length && shared.every((row, index) => row === previous[index])
		? previous as T[] : shared;
}
