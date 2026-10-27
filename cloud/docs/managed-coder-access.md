# AO managed Coder access

Untagged organizations can run sessions on AO's managed Coder and on
Freestyle, both on Stripe plans. Tagging a person's WorkOS organization with
`capabilities = coder` makes AO's managed Coder its default and free, and lets
it connect its own Coder deployment. Freestyle stays on offer, on a plan. Anyone with WorkOS
dashboard access can set the tag.

## Give an email free AO managed Coder

1. Open the WorkOS dashboard, then **Users**, and search for the email.
2. Open the user and find their **Organization** under memberships.
   - Most users already have one, named after their email.
   - No organization? Go to **Organizations**, create one named after the
     email, and add the user as a member with the Admin role.
3. Open that organization and find **Metadata**.
4. Add the key `capabilities` with the value `coder`. Leave every other key
   alone.
5. Save. The change applies within 5 minutes. They only need to sign out
   and back in if you had to create the organization in step 2.

That account now starts on AO's managed Coder, free. It can still buy a plan to
use Freestyle. Sessions run on AO's own Coder; there is nothing to set up in
Coder itself.

## Take it away again

1. In WorkOS, open the same organization and its **Metadata**.
2. Delete the `capabilities` key, then save.

Within 5 minutes the account is like any new user: it is offered Freestyle as
well, and new Coder sessions need a plan, the same as Freestyle. Coder
sessions it already has keep running and stay free, since a session's billing
is fixed when it starts. Projects keep the sandbox they were created on; the
organization's default only applies to new projects, and an admin changes it
in Settings, General, Cloud.

## What each setting gives

| `capabilities` on the organization | Starts on | Coder | Freestyle |
| --- | --- | --- | --- |
| `coder` | Coder | Free, plus its own Coder deployment | Stripe plans |
| not set (the default for everyone) | Freestyle | Stripe plans | Stripe plans |

With both providers, an admin picks the default in Settings, General, Cloud.
Use `coder` for internal people and Coder customers.

## Good to know

- **It's per organization, not per email.** The tag sits on the WorkOS
  organization the person signs in with. Their other emails have their own
  organizations, so they stay on Freestyle.
- **Changes take up to 5 minutes.** AO caches organization settings briefly.
  If the app still shows the old choice, reload it.
- **Staging and production share one WorkOS.** A tag set once applies to both.
- **Do not edit other metadata keys** (such as `billing` or
  `sandbox_provider`) unless asked.
- **Freestyle needs a plan for everyone**, internal people and organizations
  on the `internal` plan or `billing=contract` included. Those only keep
  paywalled features without a plan.
- **Using your own Coder** (a customer bringing their own) is a different
  setup, done in AO's Settings, Coder page. This runbook only covers AO's
  managed Coder.
