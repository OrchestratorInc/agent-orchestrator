type WithFields<Result, Fields> = Result extends unknown ? Omit<Result, keyof Fields> & Fields : never;

/**
 * A TanStack Query result with some fields replaced, as `{ ...result, ...fields }`
 * would give, without reading the fields the caller does not use.
 *
 * useQuery re-renders a component only when a field the component read
 * changes. A spread reads every field, so the component also re-renders each
 * time a refetch starts and ends (isFetching, fetchStatus, dataUpdatedAt), even
 * when the data comes back the same. This reads each field from the result only
 * when the caller does.
 */
export function withQueryFields<Result extends object, Fields extends { [Key in keyof Result]?: unknown }>(
	result: Result,
	fields: Fields,
): WithFields<Result, Fields> {
	return new Proxy(result, {
		get: (target, key, receiver) =>
			Object.hasOwn(fields, key) ? fields[key as keyof Fields] : Reflect.get(target, key, receiver),
	}) as unknown as WithFields<Result, Fields>;
}
