#!/bin/sh
# Apply the post-publication protection baseline to this repository.
#
# Repository settings are an external mutation. AGENTS.md reserves them for an
# explicitly authorized maintainer action, so this script plans by default and
# changes nothing until it is run with --apply.
#
# Branch rulesets and secret scanning require a public repository on a free
# plan. Run this immediately after the visibility flip, not before.

set -eu

REPO="${DEVCREW_HARDEN_REPO:-comisai/comis-dev-crew}"
RULESET_NAME="main-protection"
APPLY=0

usage() {
	cat <<'USAGE'
Usage: .github/harden-repository.sh [--apply] [--repo OWNER/NAME]

  --apply         Perform the changes. Without it, the script only reports
                  what it would do and exits without mutating anything.
  --repo          Target repository. Defaults to comisai/comis-dev-crew, or
                  the DEVCREW_HARDEN_REPO environment variable.
  -h, --help      Show this message.

Requires the gh CLI, authenticated with an account holding admin on the
target repository.
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
	--apply) APPLY=1 ;;
	--repo)
		[ $# -ge 2 ] || { echo "--repo requires a value" >&2; exit 2; }
		REPO="$2"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done

step() { printf '\n== %s\n' "$1"; }
planned() { printf '   would %s\n' "$1"; }
changed() { printf '   %s\n' "$1"; }

# --- Preflight -------------------------------------------------------------

command -v gh >/dev/null 2>&1 || {
	echo "gh CLI is required: https://cli.github.com" >&2
	exit 1
}
gh auth status >/dev/null 2>&1 || {
	echo "gh is not authenticated. Run: gh auth login" >&2
	exit 1
}

visibility=$(gh api "repos/$REPO" --jq .visibility)
permission=$(gh api "repos/$REPO" --jq .permissions.admin)

printf 'repository:  %s\n' "$REPO"
printf 'visibility:  %s\n' "$visibility"
printf 'mode:        %s\n' "$([ "$APPLY" -eq 1 ] && echo apply || echo 'plan only (pass --apply to change settings)')"

[ "$permission" = "true" ] || {
	echo "the authenticated account does not hold admin on $REPO" >&2
	exit 1
}

if [ "$visibility" != "public" ]; then
	cat >&2 <<EOF

$REPO is $visibility. On a free plan, branch rulesets and secret scanning are
available only on public repositories; GitHub rejects them with HTTP 403 while
the repository is private. Publish the repository first, then re-run this.
EOF
	exit 1
fi

# --- Security features -----------------------------------------------------

step "Secret scanning and push protection"
if [ "$APPLY" -eq 1 ]; then
	gh api --method PATCH "repos/$REPO" --input - >/dev/null <<'JSON'
{
  "security_and_analysis": {
    "secret_scanning": { "status": "enabled" },
    "secret_scanning_push_protection": { "status": "enabled" }
  }
}
JSON
	changed "enabled secret scanning and push protection"
else
	planned "enable secret scanning and push protection"
fi

step "Dependabot vulnerability alerts"
if [ "$APPLY" -eq 1 ]; then
	gh api --method PUT "repos/$REPO/vulnerability-alerts" >/dev/null
	changed "enabled vulnerability alerts"
else
	planned "enable vulnerability alerts"
fi

# Automated security fixes stay off on purpose. AGENTS.md admits a dependency
# only for a concrete caller, exact-pinned, with its provenance and license
# review recorded in DEPENDENCIES.md. Auto-raised bump pull requests would
# arrive without that record. Alerts inform; a human still does the review.

# --- Merge hygiene ---------------------------------------------------------

step "Merge settings"
if [ "$APPLY" -eq 1 ]; then
	gh api --method PATCH "repos/$REPO" --input - >/dev/null <<'JSON'
{
  "allow_merge_commit": false,
  "allow_squash_merge": true,
  "allow_rebase_merge": true,
  "delete_branch_on_merge": true,
  "allow_forking": true
}
JSON
	changed "squash and rebase only, branches deleted on merge, forking allowed"
else
	planned "disable merge commits, delete branches on merge, allow forking"
fi

# --- Branch ruleset --------------------------------------------------------
#
# Required contexts are the job names in .github/workflows/ci.yml and
# security.yml. Matrix jobs expand to one context per leg. Keep this list in
# step with those workflows: a required context that never reports leaves every
# pull request queued forever.

ruleset_payload() {
	cat <<JSON
{
  "name": "$RULESET_NAME",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {
    "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_linear_history" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["squash", "rebase"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "Repository hygiene" },
          { "context": "Static architecture" },
          { "context": "Unit and coverage" },
          { "context": "Race" },
          { "context": "Protocol conformance" },
          { "context": "Integration (ubuntu-24.04)" },
          { "context": "Integration (macos-15)" },
          { "context": "Build portability" },
          { "context": "Packaged smoke (ubuntu-24.04)" },
          { "context": "Packaged smoke (macos-15)" },
          { "context": "Vulnerability, license, and secret policy" },
          { "context": "Dependency review" },
          { "context": "CodeQL" }
        ]
      }
    }
  ]
}
JSON
}

step "Branch ruleset \"$RULESET_NAME\" on the default branch"
existing=$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name == \"$RULESET_NAME\") | .id" 2>/dev/null || true)

if [ "$APPLY" -eq 1 ]; then
	if [ -n "$existing" ]; then
		ruleset_payload | gh api --method PUT "repos/$REPO/rulesets/$existing" --input - >/dev/null
		changed "updated existing ruleset $existing"
	else
		ruleset_payload | gh api --method POST "repos/$REPO/rulesets" --input - >/dev/null
		changed "created ruleset"
	fi
	changed "pull request required; force-push, deletion, and merge commits blocked"
	changed "13 status checks required, branch must be current before merge"
else
	if [ -n "$existing" ]; then
		planned "update existing ruleset $existing"
	else
		planned "create the ruleset"
	fi
	planned "require a pull request and 13 passing checks on the default branch"
	planned "block force-push, deletion, merge commits, and non-linear history"
fi

# --- Verify ----------------------------------------------------------------

if [ "$APPLY" -eq 0 ]; then
	printf '\nPlan complete. Nothing was changed. Re-run with --apply to enforce.\n'
	exit 0
fi

step "Resulting state"
gh api "repos/$REPO" --jq '"secret scanning:      \(.security_and_analysis.secret_scanning.status)
push protection:      \(.security_and_analysis.secret_scanning_push_protection.status)
merge commits:        \(.allow_merge_commit)
delete branch:        \(.delete_branch_on_merge)"' | sed 's/^/   /'

if gh api "repos/$REPO/vulnerability-alerts" >/dev/null 2>&1; then
	printf '   vulnerability alerts: enabled\n'
else
	printf '   vulnerability alerts: DISABLED\n'
fi

gh api "repos/$REPO/rulesets" \
	--jq ".[] | select(.name == \"$RULESET_NAME\") | \"ruleset:              \(.name) (\(.enforcement))\"" |
	sed 's/^/   /'

printf '\nDone. Direct pushes to the default branch are now refused for everyone,\n'
printf 'including admins. A repository admin can still edit or delete the ruleset.\n'
