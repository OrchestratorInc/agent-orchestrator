import { useUsageLimitContinue } from "../../hooks/useUsageLimitContinue";
import { UsageLimitNotice, type UsageLimitNoticeProps } from "./UsageLimitNotice";

/**
 * The composer's notice for the latest usage-limited turn, with "Continue on
 * <account>". Mounted only while such a turn exists, so an ordinary chat never
 * reads accounts.
 */
export function UsageLimitDock({ sessionId, harness, turnId, continueEnabled, retry, ...notice }: Omit<UsageLimitNoticeProps, "continueOn"> & {
	sessionId: string;
	harness: string;
	turnId: string;
	/** Off for remote and cloud sessions, and once the chat already moved. */
	continueEnabled: boolean;
	retry: (turnId: string) => void | Promise<unknown>;
}) {
	const continueOn = useUsageLimitContinue({ sessionId, harness, limitedTurnId: turnId, enabled: continueEnabled, retry });
	return (
		<UsageLimitNotice
			{...notice}
			continueOn={continueOn
				? { account: continueOn.account, onContinue: continueOn.start, pending: continueOn.pending, error: continueOn.error }
				: undefined}
		/>
	);
}
