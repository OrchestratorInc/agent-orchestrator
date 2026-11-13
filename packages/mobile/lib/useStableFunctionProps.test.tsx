import { act, create } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useStableFunctionProps } from "./useStableFunctionProps";

type Props = { label: string; onPress?: () => string };
let latest!: Props;
function Harness(props: Props) {
	latest = useStableFunctionProps(props);
	return null;
}

beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => vi.unstubAllGlobals());

it("keeps a handler's identity across renders and always calls the newest one", async () => {
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<Harness label="a" onPress={() => "first"} />); });
	const first = latest.onPress;
	await act(async () => renderer.update(<Harness label="b" onPress={() => "second"} />));
	expect(latest.onPress).toBe(first);
	expect(latest.onPress?.()).toBe("second");
	expect(latest.label).toBe("b");
});

it("still changes when a handler comes or goes, for children that show a control only with one", async () => {
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<Harness label="a" onPress={() => "x"} />); });
	await act(async () => renderer.update(<Harness label="a" />));
	expect(latest.onPress).toBeUndefined();
});
