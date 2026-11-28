import { useRouter } from "expo-router";
import type { ReactElement } from "react";
import { rejectedMachineCopy, rejectionRepairsWithQr } from "./hostRoute";
import { useApp } from "./store";
import { Button, EmptyState } from "./ui";

/**
 * What a screen with nothing to show yet says when its machine refused this
 * phone (machineState "rejected"): the board's own copy for that refusal, and
 * for a rejected pairing the repair Settings offers, scanning a new code for
 * this machine. Nothing here asks the machine again: a retry would only spend
 * another attempt toward the daemon's lockout. `action` is a screen's own way
 * out, shown after the repair.
 */
export function MachineRejectedState({ action }: { action?: ReactElement }) {
	const { currentHostId, errorStatus, config } = useApp();
	const router = useRouter();
	const copy = rejectedMachineCopy(errorStatus, config?.host);
	const repair = currentHostId && rejectionRepairsWithQr(errorStatus)
		? <Button key="scan" title="Scan new code" icon="qrScanner" onPress={() => router.push({ pathname: "/pair", params: { repairHostId: currentHostId } })} />
		: null;
	const actions = [repair, action].filter((item): item is ReactElement => Boolean(item));
	return <EmptyState icon={copy.icon} title={copy.title} message={copy.message} action={actions.length ? actions : undefined} />;
}
