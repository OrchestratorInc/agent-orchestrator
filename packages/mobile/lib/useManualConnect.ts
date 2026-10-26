import { useEffect, useState } from "react";
import { Platform } from "react-native";
import { ApiError, pingServer } from "./api";
import { DEFAULT_CONFIG, saveConfig, type ServerConfig } from "./config";
import { configForEndpoint } from "./connect";
import { isValidPort, portError } from "./connectionFields";
import { classifyConnectionFailure, describeConnectionFailure, isLocalNetworkHost } from "./connectionError";
import { probeEndpoint, probeIdentity } from "./connectRuntime";
import { normalizeServerHost } from "./endpoints";
import { haptics } from "./haptics";
import { renameHost, saveHost, setActiveHost, type Host } from "./hosts";
import { setLocalNetworkConfirmed } from "./localNetworkStore";
import { AccountManagedHostError, adoptManualConnection, DifferentMachineError, persistManualHostEdit } from "./manualConnect";
import { IncompatibleHostVersionError } from "./race";
import { MOBILE_EVENTS } from "./telemetry/events";
import { mobileTelemetry } from "./telemetry/runtime";

export type ManualConnectProps = {
	onConnected: () => void;
	/** Told when a connect or save starts and ends, so the route can hold its sheet. */
	onBusyChange?: (busy: boolean) => void;
	editingHost?: Host;
	editingEndpointIndex?: number;
};

/**
 * Why the last connect failed, and the field it belongs to: a rejected
 * password is the password's problem, anything else the address's.
 */
export type ManualConnectFailure = {
	message: string;
	field: "host" | "password";
	showLocalNetworkHint: boolean;
};

/**
 * The manual connection form's state and its Connect (or Save) action. The
 * typing fallback behind the QR scanner, also reused to edit a pairing. The
 * legacy mux port is unused against the Go daemon, so it stays hidden.
 */
export function useManualConnect({ onConnected, onBusyChange, editingHost, editingEndpointIndex = 0 }: ManualConnectProps) {
	// New pairings start blank; editing preloads only the pairing the user selected.
	const [cfg, setCfg] = useState<ServerConfig>(() => {
		const endpoint = editingHost?.endpoints[editingEndpointIndex];
		return endpoint ? configForEndpoint(endpoint, editingHost.token, editingHost.id) : { ...DEFAULT_CONFIG, password: editingHost?.token ?? "" };
	});
	const [machineName, setMachineName] = useState(editingHost?.name ?? "");
	const [busy, setBusy] = useState(false);
	useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);
	const [failure, setFailure] = useState<ManualConnectFailure | null>(null);

	// Editing a field clears the error shown against the form; the next connect says again.
	const setField = (key: "host" | "httpPort" | "password") => (value: string) => {
		setCfg((prev) => ({ ...prev, [key]: value }));
		setFailure(null);
	};
	const setSecure = (secure: boolean) => {
		setCfg((prev) => ({ ...prev, secure }));
		setFailure(null);
	};

	async function connect() {
		setBusy(true);
		setFailure(null);
		const target = { ...cfg, host: normalizeServerHost(cfg.host), httpPort: cfg.httpPort.trim() };
		try {
			if (editingHost) {
				await persistManualHostEdit(editingHost, target, machineName, editingEndpointIndex, {
					probe: (endpoint) => probeEndpoint(endpoint, new AbortController().signal),
					ping: pingServer,
					saveHost,
					renameHost,
				});
				if (isLocalNetworkHost(target.host)) void setLocalNetworkConfirmed();
				haptics.success();
				onConnected();
				return;
			}
			// Identity is public; reject unsupported hosts before presenting a password.
			let hostId = "";
			try { hostId = await probeIdentity(target); }
			catch (error) { if (error instanceof IncompatibleHostVersionError) throw error; }
			// Verify BEFORE persisting. pingServer takes the config it is handed, so
			// nothing needs to be saved to test it — and saving first would leave
			// known-bad credentials on disk. The background poller retries every 8s,
			// and the daemon locks a device out for a minute after 5 failed auths,
			// so a persisted wrong password locks the user out on its own in ~40s,
			// with no further input from them.
			await pingServer(target);
			// Reaching a LAN address proves iOS granted Local Network access, so a
			// later scan that reaches nothing can say so at once (localNetworkWait).
			if (isLocalNetworkHost(target.host)) void setLocalNetworkConfirmed();
			await saveConfig(target);
			// Store it as a machine and select it. Writing only the legacy config
			// left resolution reconnecting the previously active machine on the
			// next launch, so a manual connection silently did not stick.
			await adoptManualConnection(target, {
				identity: async () => hostId,
				saveHost,
				setActiveHost,
			}, machineName);
			mobileTelemetry()?.capture(MOBILE_EVENTS.paired, { method: "manual" });
			haptics.success();
			onConnected();
		} catch (e) {
			haptics.warning();
			if (e instanceof DifferentMachineError || e instanceof AccountManagedHostError) {
				setFailure({ message: e.message, field: "host", showLocalNetworkHint: false });
				return;
			}
			const status = e instanceof IncompatibleHostVersionError ? 426 : e instanceof ApiError ? e.status : undefined;
			const kind = classifyConnectionFailure(status);
			const copy = describeConnectionFailure(kind, {
				host: target.host,
				port: target.httpPort,
				platform: Platform.OS,
			});
			setFailure({
				message: editingHost && kind === "auth" ? "That password was rejected. Check it and try again." : copy.message,
				field: kind === "auth" ? "password" : "host",
				showLocalNetworkHint: copy.showLocalNetworkHint,
			});
		} finally {
			setBusy(false);
		}
	}

	const validPort = isValidPort(cfg.httpPort);
	const canConnect = !!machineName.trim() && !!cfg.host.trim() && validPort;
	const editing = !!editingHost;
	return {
		cfg,
		setField,
		setSecure,
		machineName,
		setMachineName,
		busy,
		failure,
		portError: portError(cfg.httpPort),
		canConnect,
		connect,
		editing,
		title: editing ? "Edit connection" : "Connect manually",
		subtitle: editing
			? "Update this machine's name, address, port, password, or TLS setting."
			: "Name this machine, then enter the address and password from Connect Mobile or ao remote-host enable.",
	};
}
