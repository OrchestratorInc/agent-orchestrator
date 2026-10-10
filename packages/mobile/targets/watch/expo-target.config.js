/** @type {import('@bacons/apple-targets/app.plugin').Config} */
module.exports = {
  type: "watch",
  name: "AOWatch",
  displayName: "AO",
  bundleIdentifier: ".watch",
  deploymentTarget: "10.0",
  icon: "../../assets/icon.png",
  frameworks: ["SwiftUI", "WatchConnectivity", "WidgetKit"],
  entitlements: { "com.apple.security.application-groups": ["group.aoagents.ao.watch"] },
};
