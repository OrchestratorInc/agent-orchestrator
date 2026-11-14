export function parseGoVersion(value) {
	const match = /\bgo(\d+)\.(\d+)(?:\.(\d+))?(?![\w.])/.exec(value);
	if (!match) return null;
	return [Number(match[1]), Number(match[2]), Number(match[3] ?? 0)];
}

export function parseMinimumGoVersion(goMod) {
	const match = /^go\s+(\d+)\.(\d+)(?:\.(\d+))?\s*$/m.exec(goMod);
	if (!match) return null;
	return [Number(match[1]), Number(match[2]), Number(match[3] ?? 0)];
}

export function meetsMinimumVersion(actual, minimum) {
	for (let index = 0; index < minimum.length; index += 1) {
		if (actual[index] !== minimum[index]) return actual[index] > minimum[index];
	}
	return true;
}
