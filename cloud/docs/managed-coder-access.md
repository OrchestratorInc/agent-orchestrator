# AO managed Coder access

Anyone with WorkOS dashboard access can put an email on AO's managed Coder by
tagging that person's WorkOS organization with `capabilities = coder`.
Untagged accounts get Freestyle, like every other user.

## Give an email AO managed Coder

1. Open the WorkOS dashboard, then **Users**, and search for the email.
2. Open the user and find their **Organization** under memberships.
   - Most users already have one, named after their email.
   - No organization? Go to **Organizations**, create one named after the
     email, and add the user as a member with the Admin role.
3. Open that organization and find **Metadata**.
4. Add the key `capabilities` with the value `coder`. Leave every other key
   alone.
5. Save. The person gets Coder within 5 minutes. They only need to sign out
   and back in if you had to create the organization in step 2.

That account now sees AO managed Coder and no Freestyle. Sessions run on AO's
own Coder; there is nothing to set up in Coder itself.

## Take it away again

1. In WorkOS, open the same organization and its **Metadata**.
2. Delete the `capabilities` key, then save.

Within 5 minutes the account is back to Freestyle, like any new user. Coder
sessions it already has keep running, but it cannot start new Coder sessions.

## What each setting gives

| `capabilities` on the organization | Sessions run on | Billing |
| --- | --- | --- |
| `coder` | AO managed Coder only | None |
| not set (the default for everyone) | Freestyle only | Stripe plans (Free, Starter, Pro) |
| `coder,freestyle` | Both, picked per project | Stripe plans for Freestyle |

Use `coder` for internal people. Only use `coder,freestyle` if someone needs
both on one account.

## Good to know

- **It's per organization, not per email.** The tag sits on the WorkOS
  organization the person signs in with. Their other emails have their own
  organizations, so they stay on Freestyle.
- **Changes take up to 5 minutes.** AO caches organization settings briefly.
  If the app still shows the old choice, reload it.
- **Staging and production share one WorkOS.** A tag set once applies to both.
- **Do not edit other metadata keys** (such as `billing` or
  `sandbox_provider`) unless asked.
- **Internal people on Freestyle** (their other emails) are billed like anyone
  else unless their organization is on the `internal` plan, which an operator
  sets in the control plane database (`ao_organizations.plan = 'internal'`).
- **Using your own Coder** (a customer bringing their own) is a different
  setup, done in AO's Settings, Coder page. This runbook only covers AO's
  managed Coder.
