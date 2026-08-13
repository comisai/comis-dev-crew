# Repository hardening

This repository currently has no server-side protection. Its engineering rules
live in [AGENTS.md](../AGENTS.md) and are enforced by convention plus a local
`.githooks/pre-push` hook, which is opt-in per clone and skippable with
`git push --no-verify`. Nothing on GitHub refuses a direct push to the default
branch.

[.github/harden-repository.sh](../.github/harden-repository.sh) closes that gap.

## Run it after publication, not before

Branch rulesets and secret scanning are available on a free plan only for public
repositories. While this repository is private, GitHub answers the relevant
endpoints with:

```text
Upgrade to GitHub Pro or make this repository public to enable this feature. (403)
```

The script checks visibility first and refuses to continue rather than failing
part-applied. Publishing the repository is what makes this baseline reachable, so
run the script immediately after the visibility change.

## Usage

The script plans by default and changes nothing. Repository settings are an
external mutation, which [AGENTS.md](../AGENTS.md) reserves for an explicitly
authorized maintainer action, so applying is always a deliberate second step.

```text
.github/harden-repository.sh                 # report the plan, change nothing
.github/harden-repository.sh --apply         # enforce it
.github/harden-repository.sh --repo O/N      # target another repository
```

It requires the `gh` CLI authenticated as an account holding admin on the target.
Re-running is safe: an existing `main-protection` ruleset is updated in place
rather than duplicated.

## What it configures

**Secret scanning and push protection.** Push protection rejects a commit that
carries a recognized credential before it reaches the remote. This matters most
for a repository whose protocol states that secrets are references that never
appear in tracked files, arguments, logs, reports, or fixtures.

**Dependabot vulnerability alerts.** `make vulncheck` runs `govulncheck` during
CI, which catches a vulnerable dependency only when CI happens to run. Alerts
cover the window where a new advisory lands against pins that have not changed.

**Merge settings.** Squash and rebase only, with merge commits disabled to match
the required linear history, and branches deleted on merge. Forking is not set
here: GitHub accepts `allow_forking` only on an org-owned private repository and
answers HTTP 422 otherwise, and a public repository already permits forks.

**A ruleset named `main-protection` on the default branch**, active with no bypass
actors:

| Rule | Effect |
| --- | --- |
| `pull_request` | No direct pushes; every change arrives through a pull request |
| `required_status_checks` | All 13 CI and security contexts must pass, and the branch must be current |
| `non_fast_forward` | Force-push refused |
| `deletion` | Branch deletion refused |
| `required_linear_history` | No merge commits |

The approval count is zero. A sole maintainer cannot approve their own pull
request, so requiring one approval would block every merge. The pull request
flow and the status checks still apply; only the second pair of eyes is absent
until there is a second maintainer, at which point raise
`required_approving_review_count` to 1 and consider adding a `CODEOWNERS` file.

With no bypass actors, the rules apply to admins too. That is deliberate:
[AGENTS.md](../AGENTS.md) forbids committing production work directly to `main`.
An admin who genuinely needs an escape hatch can still edit or delete the ruleset
in repository settings, so this is a guardrail rather than a lockout.

## What it deliberately leaves off

Dependabot **automated security fixes** stay disabled. AGENTS.md admits a
dependency only for a concrete caller, exact-pinned, with its provenance and
license review recorded in [DEPENDENCIES.md](../DEPENDENCIES.md). Auto-raised
version bumps arrive without that record and would either sit unmergeable or
tempt someone to merge around the policy. Alerts inform a human; the human does
the review.

## Keeping the required checks correct

The 13 required contexts are the job names in
[ci.yml](../.github/workflows/ci.yml) and
[security.yml](../.github/workflows/security.yml), with matrix jobs expanded to
one context per leg:

```text
Repository hygiene                          Build portability
Static architecture                         Packaged smoke (ubuntu-24.04)
Unit and coverage                           Packaged smoke (macos-15)
Race                                        Vulnerability, license, and secret policy
Protocol conformance                        Dependency review
Integration (ubuntu-24.04)                  CodeQL
Integration (macos-15)
```

Renaming a job, adding a matrix leg, or removing a job desynchronizes this list.
A required context that no workflow reports leaves every pull request queued
forever, so change the workflow and the script in the same commit.

## Verifying

`--apply` prints the resulting state. To re-check later:

```text
gh api repos/comisai/comis-dev-crew --jq '.security_and_analysis'
gh api repos/comisai/comis-dev-crew/rulesets
gh api repos/comisai/comis-dev-crew/vulnerability-alerts   # 204 when enabled
```

## Reverting

```text
gh api repos/comisai/comis-dev-crew/rulesets --jq '.[] | select(.name=="main-protection") | .id'
gh api --method DELETE repos/comisai/comis-dev-crew/rulesets/RULESET_ID
```

To soften rather than remove it, set the ruleset's enforcement to `evaluate`,
which reports what would have been blocked without blocking it.
