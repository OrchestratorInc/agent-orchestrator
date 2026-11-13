import * as Clipboard from "expo-clipboard";
import * as Crypto from "expo-crypto";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, Platform, StyleSheet, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { backOr } from "../../../../lib/backNavigation";
import {
	CLAUDE_LOOPBACK_PATH,
	claudeLoopbackRedirect,
	CODEX_LOOPBACK_PATH,
	CODEX_LOOPBACK_PORT,
	exchangeClaudeCode,
	finishClaudeLogin,
	finishCodexBrowserLogin,
	finishCodexLogin,
	startClaudeLogin,
	startCodexBrowserLogin,
	startCodexLogin,
	waitForCodexApproval,
	type ClaudeLogin,
	type CodexBrowserLogin,
	type CodexDeviceCode,
	type LoginDeps,
} from "../../../../lib/cloud/agentLogin";
import {
	agentConnection,
	agentConnectionState,
	CLOUD_AGENT_LABELS,
	CLOUD_CREDENTIAL_METHODS,
	credentialTypeLabel,
	isCloudAgent,
	type AgentConnectionState,
	type CloudCredentialMethod,
} from "../../../../lib/cloud/agents";
import { BrowserSignInUnavailable, canSignInWithBrowser, openSignedInBrowser, prepareBrowserSignIn, type BrowserSignIn } from "../../../../lib/cloud/browserSignIn";
import { CloudSignIn } from "../../../../lib/cloud/components";
import { useCloud } from "../../../../lib/cloud/CloudProvider";
import { CredentialFieldSection } from "../../../../lib/cloud/CredentialFieldSection";
import { cloudErrorMessage } from "../../../../lib/cloud/errors";
import { FormPasswordField, FormTextField } from "../../../../lib/formFields";
import { haptics } from "../../../../lib/haptics";
import { SettingsButtonRow, SettingsForm, SettingsSection, SettingsValueRow, type SettingsTone } from "../../../../lib/SettingsForm";
import { Spinner } from "../../../../lib/Spinner";
import { useTheme } from "../../../../lib/ThemeProvider";
import { ToastHost } from "../../../../lib/Toast";
import { toolbarIcon } from "../../../../lib/toolbarIcon";
import { Button, EmptyState } from "../../../../lib/ui";
import { useGlassBarInset } from "../../../../lib/useHeaderOverlap";
import { useToast } from "../../../../lib/useToast";

export { RouteErrorBoundary as ErrorBoundary } from "../../../../lib/RouteErrorBoundary";

const loginDeps: LoginDeps = {
	fetch: (input, init) => fetch(input, init),
	randomBytes: (count) => Crypto.getRandomBytes(count),
	sha256Base64: (input) => Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, input, { encoding: Crypto.CryptoEncoding.BASE64 }),
	sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
};

type Flow =
	| { kind: "choose" }
	| { kind: "browser"; method: CloudCredentialMethod; cancel(): void; saving?: boolean }
	| { kind: "paste"; method: CloudCredentialMethod }
	| { kind: "claude"; method: CloudCredentialMethod; login: ClaudeLogin; attempt: number }
	| { kind: "codex"; method: CloudCredentialMethod; device: CodexDeviceCode; saving?: boolean };

/** What the field holding a pasted credential is called. */
function fieldLabel(method: CloudCredentialMethod): string {
	if (method.multiline) return "auth.json";
	if (method.credentialType === "oauth_token") return "Token";
	return "API key";
}

/** The saved credential's state, as the page's first row says it. */
function describeState(state: AgentConnectionState | null): { value: string; tone: SettingsTone; note?: string } {
	switch (state) {
		case "connected":
			return { value: "Connected", tone: "good" };
		case "invalid":
			return { value: "Sign in again", tone: "bad", note: "The saved credential stopped working. Connect it again below." };
		case "pending":
			return { value: "Checking…", tone: "default", note: "Orchestrator Cloud is checking the saved credential with the provider." };
		case "none":
			return { value: "Not connected", tone: "default" };
		default:
			return { value: "Checking…", tone: "default" };
	}
}

/**
 * One coding agent's credential for Orchestrator Cloud: the person's own
 * subscription sign-in or API key, validated by the control plane against the
 * provider before it is stored, encrypted, and shared across their devices.
 * Sessions use it through Orchestrator Cloud's model proxy, never inside the
 * sandbox.
 *
 * A sign-in runs in the browser that holds the person's sessions and comes back
 * on a loopback redirect; a build or phone that cannot listen falls back to the
 * code-based sign-in (Claude's paste-back code, ChatGPT's device code).
 */
export default function CloudAgentScreen() {
	const t = useTheme();
	const router = useRouter();
	const navigation = useNavigation();
	const glassInset = useGlassBarInset();
	const toast = useToast();
	const { agent: raw } = useLocalSearchParams<{ agent: string }>();
	const agent = String(raw ?? "");
	const { status, error: cloudError, client, account, connections, connectionsLoaded, refresh, refreshConnections } = useCloud();
	const [flow, setFlow] = useState<Flow>({ kind: "choose" });
	const [secret, setSecret] = useState("");
	const [busy, setBusy] = useState(false);
	// Which row's action is running, so that row alone shows the spinner.
	const [pending, setPending] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [retrying, setRetrying] = useState(false);
	const codexAbort = useRef<AbortController | null>(null);
	const browserCancel = useRef<(() => void) | null>(null);
	const submitRef = useRef<() => void>(() => {});
	// Each sign-in is an attempt; cancelling, going back to the choices and
	// leaving the page end it. Every step after a wait checks it is still the
	// current attempt before it opens the browser, polls or saves, so a
	// cancelled sign-in never stores its credential.
	const attempt = useRef(0);
	const beginAttempt = () => ++attempt.current;
	const owns = (id: number) => attempt.current === id;
	const endAttempt = () => { attempt.current++; };

	const label = isCloudAgent(agent) ? CLOUD_AGENT_LABELS[agent] : "Coding agent";
	// Leaving the page stops a ChatGPT code still waiting for approval, and the
	// browser sign-in's listener.
	useEffect(() => () => {
		attempt.current++;
		codexAbort.current?.abort();
		browserCancel.current?.();
	}, []);
	// A sign-in belongs to the Cloud account that started it: another account
	// signing in ends it, so its credential is never saved under that one.
	const accountId = account?.id;
	const signedInAs = useRef(accountId);
	useEffect(() => {
		if (signedInAs.current === accountId) return;
		signedInAs.current = accountId;
		attempt.current++;
		codexAbort.current?.abort();
		browserCancel.current?.();
		setBusy(false);
		setFlow({ kind: "choose" });
	}, [accountId]);

	const titleScreen = useMemo(() => <Stack.Screen options={{ title: label }} />, [label]);

	// A pasted credential or Claude's code is sent from the bar's trailing item,
	// as the machine editor saves: text on iOS, a check on Android, whose bar
	// draws icons only. Memoized on what it shows, so typing does not register
	// the header again on every key; the item sends the latest text.
	const submitTitle = flow.kind === "paste" ? "Save" : flow.kind === "claude" ? "Connect" : null;
	const canSubmit = secret.trim().length > 0 && !busy;
	const headerItems = useMemo(() => {
		if (!submitTitle) return null;
		const accessibilityLabel = submitTitle === "Save" ? `Save ${label} credential` : `Connect ${label}`;
		return (
			<Stack.Toolbar placement="right">
				{busy ? (
					<Stack.Toolbar.View hidesSharedBackground>
						<Spinner accessibilityLabel={`Connecting ${label}`} />
					</Stack.Toolbar.View>
				) : Platform.OS === "ios" ? (
					<Stack.Toolbar.Button accessibilityLabel={accessibilityLabel} disabled={!canSubmit} style={{ fontWeight: "600" }} onPress={() => submitRef.current()}>{submitTitle}</Stack.Toolbar.Button>
				) : (
					<Stack.Toolbar.Button icon={toolbarIcon("checkmark")} accessibilityLabel={accessibilityLabel} disabled={!canSubmit} onPress={() => submitRef.current()} />
				)}
			</Stack.Toolbar>
		);
	}, [busy, canSubmit, label, submitTitle]);

	const page = (content: ReactNode) => (
		<View style={[styles.fill, { backgroundColor: t.bgBase }]}>
			{titleScreen}
			{content}
		</View>
	);

	if (status === "signed-out") return page(<CloudSignIn />);
	if (status === "loading" || status === "connecting") {
		return page(<View style={[styles.fill, styles.center]}><Spinner size="large" accessibilityLabel="Loading Orchestrator Cloud" /></View>);
	}
	if (status === "error") {
		return page(
			<View style={[styles.fill, styles.center, { paddingTop: glassInset }]}>
				<EmptyState
					icon="error"
					title="Can't reach Orchestrator Cloud"
					message={cloudError ?? "Try again in a moment."}
					action={<Button title="Retry" icon="refresh" loading={retrying} onPress={() => { setRetrying(true); void refresh().finally(() => setRetrying(false)); }} />}
				/>
			</View>,
		);
	}
	if (!isCloudAgent(agent)) {
		return page(<View style={[styles.fill, styles.center, { paddingTop: glassInset }]}><EmptyState icon="error" title="Unknown coding agent" /></View>);
	}

	const state = connectionsLoaded ? agentConnectionState(connections, agent) : null;
	const how = credentialTypeLabel(agentConnection(connections, agent));
	const methods = CLOUD_CREDENTIAL_METHODS[agent];
	const provider = agent === "claude-code" ? "Claude" : "ChatGPT";

	const save = async (credentialType: string, value: string) => {
		const { providerConnection } = await client.putProviderConnection(agent, credentialType, value);
		await refreshConnections();
		if (providerConnection.validationState === "invalid") throw new Error(`${label} didn't accept that credential.`);
		haptics.success();
		Alert.alert(`${label} connected`, providerConnection.validationState === "valid"
			? "Cloud sessions can now run it with your account."
			: "Orchestrator Cloud is still checking it with the provider; it shows as connected once that finishes.");
		// Left while it saved: going back now would close whatever is on top.
		if (navigation.isFocused()) backOr(router, "/settings/cloud");
	};

	/** Runs a step of the page; a sign-in's step passes its attempt, so only the current one reports. */
	const run = async (action: () => Promise<void>, id?: number) => {
		const current = () => id === undefined || owns(id);
		setBusy(true);
		setError(null);
		try { await action(); }
		catch (cause) {
			if ((cause as { name?: string })?.name === "AbortError" || !current()) return;
			haptics.error();
			setError(cloudErrorMessage(cause));
		}
		finally { if (current()) setBusy(false); }
	};

	const backToChoose = () => {
		endAttempt();
		setFlow({ kind: "choose" });
		setSecret("");
		setError(null);
	};

	const changeSecret = (text: string) => {
		setSecret(text);
		if (error) setError(null);
	};

	const choose = (method: CloudCredentialMethod) => {
		haptics.select();
		setError(null);
		setSecret("");
		setPending(method.id);
		if (method.kind === "paste") { setFlow({ kind: "paste", method }); return; }
		if (canSignInWithBrowser()) signInWithBrowser(method);
		else signInWithCode(method);
	};

	/**
	 * One tap: the provider's page in the browser that holds the person's
	 * sessions, finishing on a redirect this phone catches. Falls back to the
	 * code-based sign-in when the listener cannot run here.
	 */
	const signInWithBrowser = (method: CloudCredentialMethod) => {
		const mine = beginAttempt();
		void run(async () => {
			let session: BrowserSignIn<ClaudeLogin> | BrowserSignIn<CodexBrowserLogin>;
			try {
				session = agent === "claude-code"
					? await prepareBrowserSignIn<ClaudeLogin>(0, CLAUDE_LOOPBACK_PATH)
					: await prepareBrowserSignIn<CodexBrowserLogin>(CODEX_LOOPBACK_PORT, CODEX_LOOPBACK_PATH);
			} catch (cause) {
				if (!owns(mine)) return;
				if (cause instanceof BrowserSignInUnavailable) { signInWithCode(method); return; }
				throw cause;
			}
			// Left or cancelled while the listener started: it stops, and nothing opens.
			if (!owns(mine)) { session.cancel(); return; }
			const cancel = () => {
				if (owns(mine)) endAttempt();
				session.cancel();
			};
			// Cancelled before the browser opened, or while the credential was
			// being exchanged, it must not open or save afterwards.
			const stillWanted = () => {
				if (!owns(mine)) throw Object.assign(new Error("Sign-in cancelled."), { name: "AbortError" });
			};
			browserCancel.current = cancel;
			setFlow({ kind: "browser", method, cancel });
			try {
				let secretValue: string;
				if (agent === "claude-code") {
					const login = await startClaudeLogin(loginDeps, claudeLoopbackRedirect(session.port));
					stillWanted();
					const code = await (session as BrowserSignIn<ClaudeLogin>).run(login);
					setFlow({ kind: "browser", method, cancel, saving: true });
					secretValue = await exchangeClaudeCode(login, code, loginDeps);
				} else {
					const login = await startCodexBrowserLogin(loginDeps);
					stillWanted();
					const code = await (session as BrowserSignIn<CodexBrowserLogin>).run(login);
					setFlow({ kind: "browser", method, cancel, saving: true });
					secretValue = await finishCodexBrowserLogin(login, code, loginDeps);
				}
				stillWanted();
				await save(method.credentialType, secretValue);
			} catch (cause) {
				// A failure before the browser opened leaves the listener up: only
				// run() stops it. A newer sign-in owns the listener, so only the
				// current one stops it here.
				if (owns(mine)) session.cancel();
				// Back to the choices, unless the page already moved on (Use a code instead).
				setFlow((current) => (current.kind === "browser" && current.cancel === cancel ? { kind: "choose" } : current));
				throw cause;
			} finally {
				if (browserCancel.current === cancel) browserCancel.current = null;
			}
		}, mine);
	};

	/** The code-based sign-in: Claude's paste-back code, or ChatGPT's device code. */
	const signInWithCode = (method: CloudCredentialMethod) => {
		setError(null);
		setSecret("");
		const mine = beginAttempt();
		if (agent === "claude-code") {
			void run(async () => {
				const login = await startClaudeLogin(loginDeps);
				if (!owns(mine)) return;
				setFlow({ kind: "claude", method, login, attempt: mine });
				void openSignedInBrowser(login.authorizeUrl);
			}, mine);
			return;
		}
		void run(async () => {
			const device = await startCodexLogin(loginDeps);
			if (!owns(mine)) return;
			setFlow({ kind: "codex", method, device });
			codexAbort.current?.abort();
			const controller = new AbortController();
			codexAbort.current = controller;
			const markSaving = (saving: boolean) => setFlow((current) => (current.kind === "codex" && current.device === device ? { ...current, saving } : current));
			// Polling continues while the person approves the code in the browser.
			void (async () => {
				try {
					const approval = await waitForCodexApproval(device, loginDeps, controller.signal);
					if (controller.signal.aborted || !owns(mine)) return;
					setBusy(true);
					markSaving(true);
					const authJson = await finishCodexLogin(approval, loginDeps);
					// Cancelled while the code was exchanged: the credential is dropped.
					if (controller.signal.aborted || !owns(mine)) return;
					await save(method.credentialType, authJson);
				} catch (cause) {
					if (controller.signal.aborted || !owns(mine) || (cause as { name?: string })?.name === "AbortError") return;
					haptics.error();
					setError(cloudErrorMessage(cause));
					markSaving(false);
				} finally {
					if (!controller.signal.aborted && owns(mine)) setBusy(false);
				}
			})();
		}, mine);
	};

	const cancelCodex = () => {
		codexAbort.current?.abort();
		setBusy(false);
		backToChoose();
	};

	const copyCode = (code: string) => {
		haptics.tap();
		void Clipboard.setStringAsync(code);
		// Android 13 and later confirm a copy themselves, and read it out.
		if (Platform.OS === "ios" || Number(Platform.Version) < 33) toast.show("Code copied.");
	};

	const disconnect = () => {
		haptics.warning();
		Alert.alert(`Disconnect ${label}?`, "New cloud sessions won't be able to run it until you connect it again, on any of your devices. Running sessions stop working.", [
			{ text: "Cancel", style: "cancel" },
			{
				text: "Disconnect",
				style: "destructive",
				onPress: () => {
					setPending("disconnect");
					void run(async () => {
						await client.deleteProviderConnection(agent);
						await refreshConnections();
						haptics.success();
					});
				},
			},
		]);
	};

	// The bar's Save or Connect sends what the field holds now.
	const current = flow;
	if (current.kind === "paste") {
		const { method } = current;
		submitRef.current = () => {
			if (!secret.trim() || busy) return;
			void run(() => save(method.credentialType, method.multiline ? secret : secret.trim()));
		};
	} else if (current.kind === "claude") {
		const { method, login, attempt: claudeAttempt } = current;
		submitRef.current = () => {
			if (!secret.trim() || busy) return;
			void run(async () => {
				const token = await finishClaudeLogin(login, secret, loginDeps);
				// Gone back, left, or another account signed in while the code was
				// exchanged: the token is dropped, never uploaded.
				if (!owns(claudeAttempt)) return;
				await save(method.credentialType, token);
			}, claudeAttempt);
		};
	} else submitRef.current = () => {};

	let form: ReactNode;
	if (current.kind === "paste") {
		const { method } = current;
		form = (
			<SettingsForm testID="cloud-agent-form">
				<CredentialFieldSection title={method.title} footer={method.description} error={error}>
					{method.multiline ? (
						<FormTextField
							label={fieldLabel(method)}
							value={secret}
							onChangeText={changeSecret}
							placeholder={method.placeholder}
							autoCapitalize="none"
							autoCorrect={false}
							autoFocus
							editable={!busy}
							error={error ?? undefined}
							testID="cloud-agent-secret"
						/>
					) : (
						<FormPasswordField
							label={fieldLabel(method)}
							value={secret}
							onChangeText={changeSecret}
							placeholder={method.placeholder}
							autoFocus
							editable={!busy}
							returnKeyType="done"
							onSubmitEditing={() => submitRef.current()}
							error={error ?? undefined}
							testID="cloud-agent-secret"
						/>
					)}
				</CredentialFieldSection>
				<SettingsSection>
					<SettingsButtonRow icon="cancel" label={methods.length > 1 ? "Other ways to connect" : "Cancel"} disabled={busy} onPress={backToChoose} testID="cloud-agent-back" />
				</SettingsSection>
			</SettingsForm>
		);
	} else if (current.kind === "claude") {
		const { login } = current;
		form = (
			<SettingsForm testID="cloud-agent-form">
				<CredentialFieldSection title="Paste the code from Claude" footer="Sign in on claude.com and authorize. Claude then shows a code: copy it and paste it here." error={error}>
					<FormTextField
						label="Code"
						value={secret}
						onChangeText={changeSecret}
						placeholder="Paste code"
						autoCapitalize="none"
						autoCorrect={false}
						editable={!busy}
						returnKeyType="done"
						onSubmitEditing={() => submitRef.current()}
						error={error ?? undefined}
						testID="cloud-agent-claude-code"
					/>
				</CredentialFieldSection>
				<SettingsSection>
					<SettingsButtonRow icon="openExternal" label="Open claude.com again" onPress={() => void openSignedInBrowser(login.authorizeUrl)} testID="cloud-agent-reopen" />
					<SettingsButtonRow icon="cancel" label="Other ways to connect" disabled={busy} onPress={backToChoose} testID="cloud-agent-back" />
				</SettingsSection>
			</SettingsForm>
		);
	} else if (current.kind === "browser") {
		const { method, cancel, saving } = current;
		form = (
			<SettingsForm testID="cloud-agent-form">
				<SettingsSection
					title={saving ? `Connecting ${label}…` : `Finish signing in to ${provider}`}
					footer={saving
						? "Checking the sign-in with the provider and saving it to Orchestrator Cloud."
						: "Your browser opened with the account you're already signed in to. Approve the access there and you'll come straight back."}
				>
					<SettingsValueRow icon="waiting" label={saving ? "Saving" : "Waiting for the browser"} loading testID="cloud-agent-waiting" />
				</SettingsSection>
				{saving ? null : (
					<SettingsSection>
						<SettingsButtonRow icon="enterCode" label="Use a code instead" onPress={() => { cancel(); signInWithCode(method); }} testID="cloud-agent-use-code" />
						<SettingsButtonRow icon="cancel" label="Cancel" onPress={() => { cancel(); backToChoose(); }} testID="cloud-agent-cancel" />
					</SettingsSection>
				)}
			</SettingsForm>
		);
	} else if (current.kind === "codex") {
		const { method, device, saving } = current;
		form = (
			<SettingsForm testID="cloud-agent-form">
				<SettingsSection
					title="Enter this code on auth.openai.com"
					footer={error ?? (saving
						? "Checking the sign-in with the provider and saving it to Orchestrator Cloud."
						: "Sign in there and enter the code; opening the page copies it for you. This page updates by itself once you approve.")}
				>
					<SettingsButtonRow icon="copy" label="Copy code" value={device.userCode} onPress={() => copyCode(device.userCode)} testID="cloud-agent-device-code" />
					<SettingsButtonRow
						icon="openExternal"
						label="Open auth.openai.com"
						onPress={() => { void Clipboard.setStringAsync(device.userCode); void openSignedInBrowser(device.verificationUrl); }}
						testID="cloud-agent-open-device-page"
					/>
					{/* A code that failed stops waiting: offer a fresh one. */}
					{error ? (
						<SettingsButtonRow icon="refresh" label="Get a new code" disabled={busy} onPress={() => signInWithCode(method)} testID="cloud-agent-new-code" />
					) : (
						<SettingsValueRow icon="waiting" label={saving ? "Saving your sign-in" : "Waiting for approval"} loading testID="cloud-agent-waiting" />
					)}
				</SettingsSection>
				<SettingsSection>
					<SettingsButtonRow icon="cancel" label="Cancel" onPress={cancelCodex} testID="cloud-agent-cancel" />
				</SettingsSection>
			</SettingsForm>
		);
	} else {
		const described = describeState(state);
		const connected = state !== null && state !== "none";
		form = (
			<SettingsForm testID="cloud-agent-form">
				<SettingsSection footer={error ?? described.note}>
					<SettingsValueRow icon="status" label="Status" value={described.value} tone={described.tone} testID="cloud-agent-status" />
					{connected && how ? <SettingsValueRow icon="credential" label="Credential" value={how} testID="cloud-agent-credential" /> : null}
				</SettingsSection>
				{/* One section per way to connect, each explained under its row. */}
				{methods.map((method, index) => (
					<SettingsSection
						key={method.id}
						title={index > 0 ? undefined : state === "invalid" ? "Connect again" : connected ? "Replace the credential" : "Connect"}
						footer={method.description}
					>
						<SettingsButtonRow
							icon={method.kind === "login" ? "account" : "credential"}
							label={method.title}
							loading={busy && pending === method.id}
							disabled={busy}
							onPress={() => choose(method)}
							testID={`cloud-agent-method-${method.id}`}
						/>
					</SettingsSection>
				))}
				{connected ? (
					<SettingsSection footer="Removes the credential from Orchestrator Cloud for all your devices.">
						<SettingsButtonRow
							icon="remove"
							label="Disconnect"
							destructive
							loading={busy && pending === "disconnect"}
							disabled={busy}
							onPress={disconnect}
							testID="cloud-agent-disconnect"
						/>
					</SettingsSection>
				) : null}
			</SettingsForm>
		);
	}

	return page(
		<>
			{headerItems}
			{/* The keyboard controller keeps Android's window from resizing for the
			    IME, so the page pads itself above the keyboard. iOS's Form keeps
			    the focused field in view itself. */}
			{Platform.OS === "android" ? (
				<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.fill}>{form}</KeyboardAvoidingView>
			) : form}
			<ToastHost toast={toast} />
		</>,
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
	center: { alignItems: "center", justifyContent: "center" },
});
