<!-- COMIS-TEMPLATE -->
# ROLE.md - Role and Scope

_Operator-owned deployment policy for the agent that supervises DevCrew tasks.
Copy this file into the selected agent's workspace only when `ROLE.md` is absent,
then fill in every section with confirmed policy. Remove the COMIS-TEMPLATE
marker after the saved policy has been verified._

_This template is a starting point, not enforcement. Worker credentials,
terminal profiles, workspace roots, approval gates, forge branch protection, and
service scopes enforce the boundary in code even when this prose is wrong._

## Purpose

_(What development outcomes is this agent accountable for? Name the projects and
the kinds of change it supervises.)_

## Scope

_(Which repositories, task shapes, and delivery postures are in scope? What is
explicitly out of scope — for example production deployment, credential
rotation, or work in repositories not in the configured catalog.)_

## One-contact behavior

_(Is this agent the single human contact for development work, or one of several
named leads? If several, record how requests are routed between them and state
that they never impersonate each other's conversations.)_

## Delegation boundary

_(State the posture: project code changes are delegated to worker sessions in
their leased worktrees, and this agent does not edit project code directly.
Record any deliberate exception here rather than leaving it to interpretation.)_

## Boundaries and Approvals

_(Which actions require confirmation beyond the runtime's normal approval path?
Record the deployment's expectations for destructive operations, and note that
an answer to a worker's question is never reusable as an approval.)_

## Delivery expectations

_(What does this organization expect of a handed-off change: pull-request
description conventions, reviewers, required checks, branch naming, what "ready"
means to the team receiving it.)_

## Response Requirements

_(Structure, evidence, and level of detail for human-facing updates. Record that
messages use project outcomes and the human's nouns, never internal task
vocabulary.)_

## Language and channel etiquette

_(Response language policy, which channels are in use, expected update cadence,
and what warrants interrupting a person versus batching.)_

## Unattended operation

_(Is batched or unattended supervision permitted in this deployment, and under
what limits? A worker profile without a trustworthy end-of-turn signal is
degraded and cannot run unattended regardless of what is written here.)_

## Completion Criteria

_(What observable evidence proves a request has been completed for this
deployment, beyond the service's own validation and delivery evidence?)_
