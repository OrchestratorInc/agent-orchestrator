/**
 * Returns a query result with `data` replaced, without reading its other fields.
 *
 * React Query re-renders a component only for the result fields it reads.
 * Spreading a result (`{ ...query, data }`) reads all of them, so the caller
 * re-renders whenever any field changes: twice per poll (`isFetching` on, then
 * off) even when the data is identical. The proxy reads a field only when the
 * caller does, which keeps that tracking per field.
 */
export function withQueryData<TResult extends object, TData>(
	result: TResult,
	data: TData,
): Omit<TResult, "data"> & { data: TData } {
	return new Proxy(result, {
		get: (target, key) => (key === "data" ? data : Reflect.get(target, key)),
	}) as unknown as Omit<TResult, "data"> & { data: TData };
}
