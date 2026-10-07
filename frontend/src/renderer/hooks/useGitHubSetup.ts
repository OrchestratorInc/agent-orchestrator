import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useInstallRunner } from "../components/InstallDependencyDialog";
import { aoBridge } from "../lib/bridge";
import { useGitHubAuthRequirement, useGitHubDeviceLogin, useSystemRequirementsGate } from "./useSystemRequirementsGate";

const GH_INSTALL_TARGET = "gh" as const;
/** The GitHub page watches long-running external work (a package install, a
 *  device-code sign-in), so its normal background poll stays deliberately slow. */
const STEP_POLL_INTERVAL_MS = 2_500;

/**
 * GitHub readiness for onboarding: install the CLI in place when it is missing,
 * then run the daemon-owned device sign-in when it is present but signed out.
 * The sign-in has no terminal; the daemon hands back the one-time code, the
 * page presents it, and GitHub opens in the browser as soon as the code is
 * ready. GitHub stays advisory beyond the page's own continue rule.
 */
export function useGitHubSetup({ poll = false }: { poll?: boolean } = {}) {
	const { t } = useTranslation();
	const gate = useSystemRequirementsGate();
	const { login, start, cancel } = useGitHubDeviceLogin();
	const signInActive = start.isPending || login.state === "starting" || login.state === "awaiting_approval";
	const gh = gate.requirements.find((requirement) => requirement.id === GH_INSTALL_TARGET);
	const auth = useGitHubAuthRequirement(signInActive);
	const authRef = useRef(auth.refetch);
	authRef.current = auth.refetch;
	const requirementsRef = useRef(gate.query.refetch);
	requirementsRef.current = gate.query.refetch;
	const startRef = useRef(start.mutate);
	startRef.current = start.mutate;
	// The gh install is a system install, the same one the startup gate runs, so
	// it uses that runner rather than a second POST-and-poll of its own.
	const installRunner = useInstallRunner(
		() => {
			void requirementsRef.current();
			startRef.current();
		},
		t("onboarding.installStartFailed"),
	);

	// No manual re-check on the GitHub page: it polls until both halves settle.
	// The CLI half matters while gh is missing (an install can land at any time)
	// and the auth half while GitHub is still signed out.
	const cliMissing = gh?.satisfied === false;
	const authSatisfied = Boolean(auth.data?.satisfied);
	// "Not yet confirmed" is not the same as "needs nothing": a probe that ran
	// before the daemon was reachable leaves gh unknown, and that has to keep
	// polling or the page sits on its checking state forever.
	const cliReady = gh?.satisfied === true;
	useEffect(() => {
		if (!poll || (cliReady && authSatisfied)) return;
		const timer = window.setInterval(() => {
			// Each half is only worth probing while it can still change. While a
			// sign-in is in flight the auth query already polls on its own.
			if (!cliReady) void requirementsRef.current();
			if (!authSatisfied && !signInActive) void authRef.current();
		}, STEP_POLL_INTERVAL_MS);
		// Approving in the browser pulls focus away from the app. Probe the moment
		// it comes back instead of waiting out the next tick.
		const onFocus = () => {
			if (!authSatisfied) void authRef.current();
		};
		window.addEventListener("focus", onFocus);
		return () => {
			window.clearInterval(timer);
			window.removeEventListener("focus", onFocus);
		};
	}, [authSatisfied, cliReady, signInActive, poll]);

	// Open GitHub once per attempt, the moment its code is ready. The button on
	// the page reopens it if the browser was closed.
	const openedAttemptRef = useRef<string | null>(null);
	useEffect(() => {
		if (login.state !== "awaiting_approval" || !login.userCode || !login.id) return;
		if (openedAttemptRef.current === login.id) return;
		openedAttemptRef.current = login.id;
		void aoBridge.app.openExternal(login.verificationUrl ?? "https://github.com/login/device");
	}, [login.id, login.state, login.userCode, login.verificationUrl]);

	// The daemon reports success as soon as gh exits; confirm it against the
	// credential probe straight away rather than waiting for the next tick.
	const succeeded = login.state === "succeeded";
	useEffect(() => {
		if (succeeded) void authRef.current();
	}, [succeeded]);

	return {
		authSatisfied,
		cliMissing,
		gh,
		install: () => installRunner.start(GH_INSTALL_TARGET),
		installError: installRunner.startError ?? null,
		installing: installRunner.running,
		job: installRunner.jobFor(GH_INSTALL_TARGET),
		login,
		requirementsQuery: gate.query,
		signIn: () => start.mutate(),
		cancelSignIn: () => cancel.mutate(),
		signInActive,
		signInError: start.isError ? start.error.message : cancel.isError ? cancel.error.message : null,
		signInPending: start.isPending,
	};
}
