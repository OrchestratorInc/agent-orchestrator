import { ApiError, pingServer } from "./api";
import { classifyConnectionFailure, describeConnectionFailure } from "./connectionError";
import { probeEndpoint } from "./connectRuntime";
import { forgetPairedHost, forgetServer } from "./disconnect";
import { MachineSaveError, type EnvironmentMachineDraft } from "./environmentEditor";
import { activeHostMetadata, loadHosts, renameHost, saveHost, updateHostAppearance, type Host } from "./hosts";
import { machineAppearance } from "./machineAppearance";
import { AccountManagedHostError, DifferentMachineError, persistManualHostEdit } from "./manualConnect";
import { IncompatibleHostVersionError } from "./race";

/**
 * Saves the machine editor's draft: the name, the icon, and the edited route.
 * A changed route or password is checked against the machine before it is
 * stored (see persistManualHostEdit). Says whether the route changed, so the
 * caller can reconnect the default machine over the new one.
 */
export async function saveEnvironmentMachine(host: Host, draft: EnvironmentMachineDraft, platform: string): Promise<{ routeChanged: boolean }> {
	const before = host.endpoints[draft.endpointIndex];
	const appearanceChanged = machineAppearance(host).symbol !== draft.appearance.symbol;
	try {
		await persistManualHostEdit(host, draft.config, draft.machineName, draft.endpointIndex, {
			probe: (endpoint) => probeEndpoint(endpoint, new AbortController().signal),
			ping: pingServer,
			saveHost,
			renameHost,
		});
		if (appearanceChanged) await updateHostAppearance(host.id, draft.appearance);
	} catch (error) {
		if (error instanceof DifferentMachineError || error instanceof AccountManagedHostError) throw new MachineSaveError(error.message, "host");
		const status = error instanceof IncompatibleHostVersionError ? 426 : error instanceof ApiError ? error.status : undefined;
		const kind = classifyConnectionFailure(status);
		if (kind === "auth") throw new MachineSaveError("That password was rejected. Check it and try again.", "password");
		const copy = describeConnectionFailure(kind, { host: draft.config.host, port: draft.config.httpPort, platform });
		throw new MachineSaveError(copy.message, "host");
	}
	const routeChanged = !before || before.host !== draft.config.host.trim() || before.port !== Number(draft.config.httpPort) ||
		before.secure !== Boolean(draft.config.secure) || host.token !== draft.config.password;
	return { routeChanged };
}

/** Forgetting a machine failed and its saved connection is still there. */
export class ForgetMachineError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "ForgetMachineError";
	}
}

export type ForgottenMachine = {
	remaining: Host[];
	/** The default from here on, if any machine is left. */
	nextDefault: Host | undefined;
	wasDefault: boolean;
	/** What to tell the person, e.g. "A forgotten. B is now the default." */
	notice: string;
};

/**
 * Forgets one machine. `knownDefaultId` is the default the screen showed,
 * which account sync may have moved since, so the default is read from
 * storage; the machine asked for is forgotten either way.
 */
export async function forgetEnvironmentMachine(host: Host, knownDefaultId: string | null): Promise<ForgottenMachine> {
	let selectedId = knownDefaultId;
	let failed = false;
	try {
		selectedId = (await activeHostMetadata())?.id ?? null;
		if (selectedId === host.id) await forgetServer(host);
		else await forgetPairedHost(host);
	} catch {
		failed = true;
	}
	let remaining: Host[];
	try {
		remaining = await loadHosts();
	} catch {
		throw new ForgetMachineError("The saved machines couldn't be read. Try again.");
	}
	if (failed && remaining.some((candidate) => candidate.id === host.id)) {
		throw new ForgetMachineError("This machine's saved connection couldn't be removed. Try again.");
	}
	const wasDefault = selectedId === host.id;
	// removeHost clears the selected pointer; loadHosts is recency-sorted,
	// matching activeHost's fallback without another keychain/network wait.
	const nextDefault = remaining.find((candidate) => candidate.id === selectedId) ?? remaining[0];
	return {
		remaining,
		nextDefault,
		wasDefault,
		notice: `${host.name} forgotten.${wasDefault && nextDefault ? ` ${nextDefault.name} is now the default.` : ""}`,
	};
}
