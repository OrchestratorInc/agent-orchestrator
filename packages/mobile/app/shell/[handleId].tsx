import { useLocalSearchParams, useRouter } from "expo-router";
import { useRef } from "react";
import { View } from "react-native";
import { hostRouteMatches, machineState } from "../../lib/hostRoute";
import { MachineRejectedState } from "../../lib/MachineRejectedState";
import TerminalSessionScreen from "../../lib/session/TerminalSessionScreen";
import { Spinner } from "../../lib/Spinner";
import { HostScope, useApp } from "../../lib/store";
import { useTheme } from "../../lib/ThemeProvider";
import { Button, EmptyState } from "../../lib/ui";
import { useGlassBarInset } from "../../lib/useHeaderOverlap";

/**
 * A Chat session's terminal escape hatch attaches the shell handle through the
 * same authenticated mux and xterm renderer as every other AO terminal. The
 * handle, not the Chat session id, identifies this PTY.
 */
export default function ShellRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ShellRouteContent /></HostScope> : <ShellRouteContent />;
}

function ShellRouteContent() {
	const { hostId: routeHostId } = useLocalSearchParams<{ hostId?: string }>();
	const app = useApp();
	const { config, currentHostId, loading, machine } = app;
	const router = useRouter();
	// Once the shell has opened, it stays through an app switch: the terminal
	// reconnects by itself, as a terminal session's does. Unmounted, it was
	// rebuilt and its screen replayed on every return.
	const openedFor = useRef<string | null>(null);
	if (config && machine) openedFor.current = machine;
	const opened = Boolean(machine) && openedFor.current === machine;
	const t = useTheme();
	const glassInset = useGlassBarInset();
	if (!hostRouteMatches(routeHostId, currentHostId)) {
		return (
			<View style={{ flex: 1, justifyContent: "center", backgroundColor: t.bgBase }}>
				{loading && !config ? <Spinner accessibilityLabel="Loading" /> : (
					<EmptyState
						icon="terminal"
						title="Shell belongs to another machine"
						message="Open it from that machine's session."
						// Back down to the board under this page, not a second board on top of it.
						action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />}
					/>
				)}
			</View>
		);
	}
	// Not for a connection an app switch closed: that one reconnects. A machine
	// that refused this phone says so rather than spinning (see machineState).
	const state = !config && !opened ? machineState(app) : "ok";
	if (state === "rejected") {
		return <View style={{ flex: 1, justifyContent: "center", backgroundColor: t.bgBase }}><MachineRejectedState /></View>;
	}
	if (state === "offline") {
		return <View style={{ flex: 1, justifyContent: "center", backgroundColor: t.bgBase }}><EmptyState icon="wifi-off" title="Machine offline" message="This shell opens once the app reconnects." /></View>;
	}
	return <View style={{ flex: 1, paddingTop: glassInset, backgroundColor: t.bgBase }}><TerminalSessionScreen /></View>;
}

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
