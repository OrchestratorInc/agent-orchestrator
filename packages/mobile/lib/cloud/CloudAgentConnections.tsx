import { CloudApiError, CloudRequestTimeoutError, type AgentLogin, type CloudClient, type RedactedProviderConnection } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, Linking, Platform, ScrollView, StyleSheet, Text, TextInput, useWindowDimensions, View } from "react-native";
import { agentLabel } from "../../../product-ui/src/agents";
import { AgentLogo } from "../AgentLogo";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { iconSize, radius, space, touchTarget, type } from "../tokens";
import { Button, HeaderIconButton } from "../ui";
import { hasConnectionForHarness } from "./agentReadiness";
import { cloudProjectSetupCache } from "./projectSetupCache";

export type CloudAgentConnectionsProps = {
	client: CloudClient;
	orgId: string | null;
	/** Includes deployment, account id and login epoch; never credential material. */
	ownerKey: string;
	isCurrent(): boolean;
	onBack?(): void;
	onChanged?(): void;
	inSheet?: boolean;
};

const agents = ["claude-code", "codex", "cursor", "opencode"] as const;
type Agent = typeof agents[number];
const connectionStatuses: Record<string, string> = { valid: "Connected", pending: "Validation pending", invalid: "Connection needs attention", expired: "Connection expired" };
const activeLogin = (login: AgentLogin | null): boolean => !!login && ["starting", "waiting", "connecting"].includes(login.status);

/** Personal credentials only. Parent sheets retain drafts while this view is open. */
export function CloudAgentConnections(props: CloudAgentConnectionsProps) {
	return <CloudAgentConnectionsView key={JSON.stringify([props.ownerKey, props.orgId])} {...props} />;
}

function CloudAgentConnectionsView({ client, orgId, ownerKey, isCurrent, onBack, onChanged, inSheet = false }: CloudAgentConnectionsProps) {
	const theme = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { height } = useWindowDimensions();
	const [connections, setConnections] = useState<RedactedProviderConnection[]>([]);
	const [harnesses, setHarnesses] = useState<string[] | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);
	const [notice, setNotice] = useState<string | null>(null);
	const [login, setLogin] = useState<AgentLogin | null>(null);
	const [busy, setBusy] = useState(false);
	const [disconnecting, setDisconnecting] = useState<Agent | null>(null);
	const [manual, setManual] = useState<Agent | null>(null);
	const [secret, setSecret] = useState("");
	const [code, setCode] = useState("");
	const owner = useMemo(() => ({
		client, orgId, active: true, focused: false, foreground: AppState.currentState === "active",
		login: null as AgentLogin | null, load: null as AbortController | null,
		action: null as AbortController | null, poll: null as AbortController | null,
		timer: null as ReturnType<typeof setTimeout> | null,
		start: null as { harness: string; key: string } | null,
		connections: [] as RedactedProviderConnection[],
		notified: new Set<string>(), isCurrent,
	}), [client, orgId, ownerKey]);
	owner.isCurrent = isCurrent;
	const ownerRef = useRef(owner); ownerRef.current = owner;
	const changedRef = useRef(onChanged); changedRef.current = onChanged;
	const ownsView = useCallback(() => owner.active && ownerRef.current === owner && owner.isCurrent(), [owner]);
	const clearPolling = useCallback(() => {
		if (owner.timer) clearTimeout(owner.timer);
		owner.timer = null; owner.poll?.abort(); owner.poll = null;
	}, [owner]);
	const cancelRemote = useCallback((id: string) => {
		// Best effort: the bound client prevents an old account using a new token.
		void owner.client.cancelAgentLogin(id).catch(() => {});
	}, [owner]);
	const refresh = useCallback(async (capabilities = false) => {
		if (!ownsView()) return;
		owner.load?.abort();
		const request = new AbortController(); owner.load = request;
		const current = () => ownsView() && owner.load === request && !request.signal.aborted;
		setLoading(true);
		const [available, native] = await Promise.allSettled([
			owner.client.listUserProviderConnections({ signal: request.signal }),
			capabilities ? owner.client.getAgentLoginCapabilities({ signal: request.signal }) : Promise.resolve(null),
		]);
		if (!current()) return;
		if (available.status === "fulfilled") { owner.connections = available.value; setConnections(available.value); }
		else setError(agentConnectionErrorCopy(available.reason, "Could not refresh connected agents. Try again."));
		if (capabilities) setHarnesses(native.status === "fulfilled" ? native.value?.harnesses.filter(harness => harness === "claude-code" || harness === "codex") ?? [] : []);
		setLoading(false); owner.load = null;
	}, [owner, ownsView]);
	const acceptLogin = useCallback(async (next: AgentLogin) => {
		if (next.status === "connected") cloudProjectSetupCache.invalidateAccountConnections(ownerKey);
		if (!ownsView()) return;
		owner.login = next; setLogin(next);
		if (!activeLogin(next)) {
			clearPolling(); setCode("");
			if (next.status === "connected" && !owner.notified.has(next.id)) {
				owner.notified.add(next.id);
				setNotice("Sign-in connected.");
				await refresh();
				if (ownsView()) changedRef.current?.();
			}
		}
	}, [owner, ownerKey, ownsView, clearPolling, refresh]);
	const pollRef = useRef<() => Promise<void>>(async () => {});
	const schedule = useCallback(() => {
		if (!ownsView() || !owner.foreground || !owner.focused || !activeLogin(owner.login)) return;
		if (owner.timer) clearTimeout(owner.timer);
		owner.timer = setTimeout(() => { owner.timer = null; void pollRef.current(); }, 1500);
	}, [owner, ownsView]);
	const poll = useCallback(async () => {
		if (!ownsView() || !owner.foreground || !owner.focused || !activeLogin(owner.login) || owner.poll) return;
		const attempt = owner.login!;
		if (Date.parse(attempt.expiresAt) <= Date.now()) {
			cancelRemote(attempt.id);
			await acceptLogin({ ...attempt, status: "expired", userCode: undefined, authorizationUrl: undefined });
			return;
		}
		const request = new AbortController(); owner.poll = request;
		try {
			const result = await owner.client.getAgentLogin(attempt.id, { signal: request.signal });
			if (!ownsView() || request.signal.aborted || owner.login?.id !== attempt.id) return;
			setError(null); await acceptLogin(result.login);
		} catch (cause) {
			if (ownsView() && !request.signal.aborted) setError(agentConnectionErrorCopy(cause, "Could not check sign-in status. Return to this screen or retry."));
		} finally {
			if (owner.poll === request) owner.poll = null;
			if (!request.signal.aborted) schedule();
		}
	}, [owner, ownsView, acceptLogin, cancelRemote, schedule]);
	pollRef.current = poll;

	useEffect(() => {
		owner.active = true;
		owner.connections = [];
		setConnections([]); setHarnesses(null); setLogin(null); setManual(null); setSecret(""); setCode(""); setError(null); setNotice(null); setBusy(false); setDisconnecting(null);
		void refresh(true);
		const listener = AppState.addEventListener("change", state => {
			owner.foreground = state === "active";
			if (owner.foreground) void pollRef.current();
			else { clearPolling(); setCode(""); setSecret(""); }
		});
		return () => {
			owner.active = false; clearPolling(); owner.load?.abort(); owner.action?.abort();
			if (activeLogin(owner.login)) cancelRemote(owner.login!.id);
			listener.remove();
		};
	}, [owner, refresh, clearPolling, cancelRemote]);
	useFocusEffect(useCallback(() => {
		owner.focused = true; void pollRef.current();
		return () => { owner.focused = false; clearPolling(); };
	}, [owner, clearPolling]));

	const cancel = useCallback(async () => {
		if (!ownsView()) return;
		const attempt = activeLogin(owner.login) ? owner.login : null;
		const pendingStart = owner.start !== null;
		clearPolling(); owner.action?.abort(); owner.action = null;
		owner.login = null; owner.start = null;
		setLogin(null); setCode(""); setSecret(""); setBusy(false); setError(null);
		setNotice(attempt ? "Cancelling sign-in…" : pendingStart ? "Sign-in closed. A pending Cloud sign-in expires automatically." : null);
		if (!attempt) return;
		try {
			await owner.client.cancelAgentLogin(attempt.id);
			if (ownsView() && !owner.login && !owner.action) setNotice("Sign-in cancelled.");
		} catch {
			if (ownsView() && !owner.login && !owner.action) {
				setNotice(null); setError("Could not confirm Cloud cancellation. Sign-in is closed on this phone; the Cloud attempt expires automatically.");
			}
		}
	}, [owner, ownsView, clearPolling]);
	const start = async (harness: Agent) => {
		if (harness !== "claude-code" && harness !== "codex") return;
		if (!ownsView() || !owner.orgId || owner.action || activeLogin(owner.login) || !harnesses?.includes(harness)) return;
		setCode(""); setSecret(""); setManual(null); setError(null); setNotice(null); setBusy(true);
		const request = new AbortController(); owner.action = request;
		if (owner.start?.harness !== harness) owner.start = { harness, key: Crypto.randomUUID() };
		try {
			const result = await owner.client.startAgentLogin({ orgId: owner.orgId, harness }, owner.start.key, { signal: request.signal });
			if (!ownsView() || request.signal.aborted) { cancelRemote(result.login.id); return; }
			owner.start = null; await acceptLogin(result.login); schedule();
		} catch (cause) {
			if (ownsView() && !request.signal.aborted) setError(agentConnectionErrorCopy(cause, "Could not start browser sign-in. Try again or use an API key."));
		} finally {
			if (owner.action === request) owner.action = null;
			if (ownsView() && !request.signal.aborted) setBusy(false);
		}
	};
	const submitCode = async () => {
		if (!ownsView() || !owner.login || !activeLogin(owner.login) || owner.action || !code.trim()) return;
		const value = code.trim(); setCode(""); setError(null); setBusy(true); clearPolling();
		const attempt = owner.login;
		const request = new AbortController(); owner.action = request;
		try {
			const result = await owner.client.submitAgentLoginCode(attempt.id, { code: value }, { signal: request.signal });
			if (ownsView() && !request.signal.aborted) await acceptLogin(result.login);
		} catch (cause) {
			if (ownsView() && !request.signal.aborted) setError(agentConnectionErrorCopy(cause, "Could not submit the sign-in code. Check the provider page and try again."));
		} finally {
			if (owner.action === request) owner.action = null;
			if (ownsView() && !request.signal.aborted) { setBusy(false); schedule(); }
		}
	};
	const saveKey = async () => {
		if (!ownsView() || !manual || !secret.trim() || owner.action) return;
		const value = secret.trim(); setSecret(""); setBusy(true); setError(null);
		const request = new AbortController(); owner.action = request;
		try {
			const { providerConnection } = await owner.client.putUserAgentConnection(manual, { credentialType: manual === "opencode" ? "opencode_api_key" : "api_key", secret: value }, { signal: request.signal });
			cloudProjectSetupCache.invalidateAccountConnections(ownerKey);
			if (!ownsView() || request.signal.aborted) return;
			setManual(null); setNotice(providerConnection.validationState === "valid" ? "API key connected." : providerConnection.validationState === "pending" ? "API key saved. Validation is pending." : "API key saved, but this connection needs attention. Reconnect or enter a new API key.");
			await refresh(); if (ownsView()) changedRef.current?.();
		} catch (cause) {
			if (ownsView() && !request.signal.aborted) setError(agentConnectionErrorCopy(cause, "Could not save this API key. Check the key and try again."));
		} finally {
			if (owner.action === request) owner.action = null;
			if (ownsView() && !request.signal.aborted) setBusy(false);
		}
	};
	const disconnect = (agent: Agent) => {
		const canDisconnect = () => ownsView() && !owner.action && !activeLogin(owner.login)
			&& owner.connections.some(item => item.provider === agent && item.label === "default");
		if (!canDisconnect()) return;
		Alert.alert(`Disconnect ${agentLabel(agent)}?`, "This removes your saved personal coding-agent credential from Cloud. Connect again before starting a new task with this agent.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Disconnect", style: "destructive", onPress: async () => {
				if (!canDisconnect()) return;
				const request = new AbortController(); owner.action = request; owner.load?.abort();
				owner.load = null; setLoading(false);
				setBusy(true); setDisconnecting(agent); setError(null); setNotice(null);
				try {
					await owner.client.deleteUserAgentConnection(agent, { signal: request.signal });
					if (!ownsView() || request.signal.aborted) return;
					cloudProjectSetupCache.invalidateAccountConnections(ownerKey);
					owner.connections = owner.connections.filter(item => item.provider !== agent || item.label !== "default");
					setConnections(owner.connections); setNotice(`${agentLabel(agent)} disconnected.`);
					await refresh(); if (ownsView()) changedRef.current?.();
				} catch (cause) {
					if (ownsView() && !request.signal.aborted) setError(agentConnectionErrorCopy(cause, "Could not disconnect this agent. Try again."));
				} finally {
					if (owner.action === request) owner.action = null;
					if (ownsView() && !request.signal.aborted) { setBusy(false); setDisconnecting(null); }
				}
			} },
		]);
	};
	const providerUrl = login && activeLogin(login) ? trustedProviderUrl(login.harness, login.authorizationUrl) : null;
	const pending = busy || activeLogin(login);
	return <ScrollView style={inSheet && Platform.OS === "android" ? { maxHeight: height * 0.7 } : undefined} nestedScrollEnabled={inSheet} contentInsetAdjustmentBehavior="automatic" automaticallyAdjustKeyboardInsets keyboardShouldPersistTaps="handled" contentContainerStyle={styles.content}>
		{onBack ? <View style={styles.header}><HeaderIconButton icon="back" label="Back to draft" onPress={() => { cancel(); onBack(); }} /><Text style={styles.heading}>Cloud coding agents</Text></View> : null}
		<Text style={styles.description}>Connect your personal coding agents to use them in Cloud projects.</Text>
		{loading ? <ActivityIndicator accessibilityLabel="Loading connected agents" color={theme.textSecondary} /> : null}
		{notice ? <Text selectable accessibilityLiveRegion="polite" style={styles.description}>{notice}</Text> : null}
		{error ? <Text selectable accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
		{harnesses?.length === 0 ? <Text selectable style={styles.description}>Browser sign-in is unavailable on this Cloud deployment. Connect an API key below, or keep using your connected agents.</Text> : null}
		{!orgId ? <Text selectable style={styles.description}>Your Cloud workspace must be ready before browser sign-in can start. API keys are personal to your account.</Text> : null}
		{login ? <View style={styles.card}>
			<Text style={styles.heading}>{agentLabel(login.harness)} sign-in</Text>
			<Text selectable accessibilityLiveRegion="polite" style={styles.description}>{loginStatus(login.status)}</Text>
			{activeLogin(login) ? <>
				{login.userCode ? <><Text style={styles.description}>Enter this code on the provider page:</Text><Text selectable style={styles.deviceCode}>{login.userCode}</Text></> : null}
				{providerUrl ? <Button title="Open provider sign-in" variant="text" onPress={() => {
					if (!ownsView()) return;
					void Linking.openURL(providerUrl).catch(() => { if (ownsView()) setError("Could not open the provider sign-in page. Try again."); });
				}} /> : login.authorizationUrl ? <Text selectable style={styles.error}>Cloud did not return a valid provider sign-in link. Cancel and try again.</Text> : null}
				{login.harness === "claude-code" && login.requiresCode ? <>
					<TextInput {...textSelectionColors(theme)} accessibilityLabel="Claude sign-in code" secureTextEntry value={code} onChangeText={setCode} autoCapitalize="none" autoCorrect={false} autoComplete="off" textContentType="oneTimeCode" style={styles.input} editable={!busy} />
					<Button title="Submit sign-in code" disabled={busy || !code.trim()} onPress={submitCode} />
				</> : null}
				{error ? <Button title="Retry sign-in status" variant="text" onPress={() => { void poll(); }} /> : null}
				<Button title="Cancel sign-in" variant="text" onPress={cancel} />
			</> : null}
		</View> : busy && !manual && !disconnecting ? <Button title="Cancel sign-in" variant="text" onPress={cancel} /> : null}
		{agents.map(agent => {
			const connected = hasConnectionForHarness(connections, agent);
			const metadata = connections.find(item => item.provider === agent && item.label === "default");
			return <View key={agent} style={styles.card}>
				<View style={styles.identity}><AgentLogo harness={agent} size={iconSize.xl} /><View style={styles.identityCopy}><Text style={styles.heading}>{agentLabel(agent)}</Text><Text style={styles.description}>{metadata ? connectionStatuses[metadata.validationState] ?? "Connection needs attention" : "Not connected"}</Text></View></View>
				{orgId && harnesses?.includes(agent) ? <Button title={`${connected ? "Reconnect" : "Connect"} ${agentLabel(agent)}`} variant="text" disabled={pending} onPress={() => start(agent)} /> : null}
				{manual === agent ? <>
					<TextInput {...textSelectionColors(theme)} accessibilityLabel={`${agentLabel(agent)} API key`} secureTextEntry value={secret} onChangeText={setSecret} autoCapitalize="none" autoCorrect={false} autoComplete="off" textContentType="password" style={styles.input} editable={!busy} />
					<Button title="Save API key" loading={busy} disabled={busy || !secret.trim()} onPress={saveKey} />
					<Button title="Cancel API key entry" variant="text" disabled={busy} onPress={() => { setSecret(""); setManual(null); }} />
				</> : <Button title={`Use ${agentLabel(agent)} API key`} variant="text" disabled={pending} onPress={() => { if (ownsView() && !pending) { setSecret(""); setCode(""); setManual(agent); setError(null); } }} />}
				{metadata ? <Button title={`Disconnect ${agentLabel(agent)}`} variant="text" disabled={pending} loading={disconnecting === agent} onPress={() => disconnect(agent)} /> : null}
			</View>;
		})}
		<Button title="Refresh connected agents" variant="text" disabled={loading || busy} onPress={() => { setError(null); void refresh(true); }} />
	</ScrollView>;
}

/** Provider errors may echo secrets; display only copy derived from typed status/code. */
function agentConnectionErrorCopy(cause: unknown, fallback: string): string {
	if (cause instanceof CloudRequestTimeoutError || cause instanceof TypeError) return "Cloud could not be reached. Check your connection and try again.";
	if (!(cause instanceof CloudApiError)) return fallback;
	if (cause.status === 401) return "Your Cloud sign-in has expired. Sign in again to manage connected agents.";
	if (cause.status === 403) return "You do not have permission to manage coding agents in this Cloud workspace.";
	if (cause.code === "SANDBOX_QUOTA_EXCEEDED") return "Cloud has reached its capacity limit for sign-in. Wait for capacity to become available, then try again.";
	if (cause.status === 402) return "Your Cloud plan does not currently allow this action. Check your plan in Cloud settings.";
	if (cause.status === 429) return "Too many sign-in requests. Wait a moment, then try again.";
	if (cause.code === "invalid_credential") return "This coding-agent credential is invalid or expired. Reconnect or enter a new API key.";
	if (cause.code === "provider_unavailable") return "The coding-agent provider is temporarily unavailable. Try again later.";
	if (cause.status === 404 || cause.status === 501 || cause.status >= 500) return "This Cloud connection service is unavailable. Try again later, or use an API key for browser sign-in.";
	return fallback;
}

function trustedProviderUrl(harness: string, value?: string): string | null {
	if (!value) return null;
	try {
		const url = new URL(value);
		if (url.protocol !== "https:" || url.username || url.password || url.port || url.hash) return null;
		if (harness === "codex") return url.origin === "https://auth.openai.com" && url.pathname === "/codex/device" && !url.search ? value : null;
		if (harness !== "claude-code") return null;
		const allowed = (url.origin === "https://claude.com" && url.pathname === "/cai/oauth/authorize") ||
			(["https://claude.ai", "https://console.anthropic.com"].includes(url.origin) && url.pathname === "/oauth/authorize");
		if (!allowed || url.searchParams.get("code") !== "true") return null;
		const redirect = url.searchParams.get("redirect_uri");
		if (!["https://platform.claude.com/oauth/code/callback", "https://console.anthropic.com/oauth/code/callback"].includes(redirect ?? "")) return null;
		return value;
	} catch { return null; }
}

function loginStatus(status: AgentLogin["status"]): string {
	return ({ starting: "Preparing a private sign-in session…", waiting: "Finish sign-in on the provider page.", connecting: "Connecting your account…", connected: "Connected", failed: "Sign-in failed. Try again or use an API key.", cancelled: "Sign-in cancelled.", expired: "Sign-in expired. Start again to get a new code." })[status];
}

const makeStyles = (theme: Theme) => StyleSheet.create({
	content: { gap: space.md, paddingBottom: space.lg },
	header: { flexDirection: "row", alignItems: "center", gap: space.sm },
	heading: { ...type.subheadline, color: theme.textPrimary },
	description: { ...type.footnote, color: theme.textSecondary },
	error: { ...type.footnote, color: theme.red },
	card: { gap: space.sm, padding: space.md, backgroundColor: theme.bgElevated, borderRadius: radius.md, borderCurve: "continuous" },
	identity: { flexDirection: "row", alignItems: "center", gap: space.md },
	identityCopy: { gap: space.xxs, flex: 1 },
	input: { ...type.body, minHeight: touchTarget, color: theme.textPrimary, borderWidth: StyleSheet.hairlineWidth, borderColor: theme.borderDefault, borderRadius: radius.sm, paddingHorizontal: space.md },
	deviceCode: { ...type.headline, color: theme.textPrimary, fontVariant: ["tabular-nums"] },
});
