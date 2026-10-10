/** @type {import('@bacons/apple-targets/app.plugin').Config} */
module.exports = {
  type: "watch-widget",
  name: "AOAttention",
  displayName: "Needs you",
  bundleIdentifier: ".watch.attention",
  deploymentTarget: "10.0",
  entitlements: { "com.apple.security.application-groups": ["group.aoagents.ao.watch"] },
};
