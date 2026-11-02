import { Tabs } from "expo-router";
import { SidebarNavigationShell } from "../../lib/sidebar-navigation-shell";

export default function SidebarLayout() {
	return (
		<SidebarNavigationShell>
			{/* Sidebar destinations are peers. Replacing stack screens destroys
			    their lists during drawer closing; retain visited tabs instead.
			    The existing sidebar remains the only tab-selection UI. */}
			<Tabs
				initialRouteName="index"
				backBehavior="none"
				tabBar={() => null}
				detachInactiveScreens
				screenOptions={{ headerShown: false, animation: "none", lazy: true, freezeOnBlur: true }}
			/>
		</SidebarNavigationShell>
	);
}
