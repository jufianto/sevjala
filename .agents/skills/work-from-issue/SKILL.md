---
name: work-from-issue
description: Use when the user asks to work on, plan, or implement a specific tracker issue (GitHub issue number, Jira key) or the next item from the project's progress file, or runs /work-from-issue. Not for ordinary small fixes with no issue behind them.
---

# Work from issue

Thin orchestrator: take one issue from tracker to merged-ready branch. Each step delegates to a named skill; do not restate what those skills do.

## Parameters

Read these from the project's `AGENTS.md`. Fallback when absent:

| Parameter | Fallback |
|---|---|
| Tracker | GitHub via `gh`. Use Jira through the Atlassian MCP only if `AGENTS.md` says so. |
| Progress file | `PROGRESS.md` if present, else none |
| Docs index | the "Start here" section of `AGENTS.md` |
| Test command | from `AGENTS.md`, else the Makefile `test` target, else the package manager's test script |
| Reviewer agent | the reviewer used by superpowers:requesting-code-review |

## Steps

1. **Fetch the issue.** GitHub: `gh issue view <N>`. Jira: fetch it through the Atlassian MCP. If no issue was given, take the next item from the progress file; if there is no progress file, ask the user which issue.
2. **Read context.** Read the docs named in the docs index that relate to the issue, plus the code it touches. Load any stack skills the project has installed.
3. **REQUIRED SUB-SKILL:** superpowers:brainstorming, then superpowers:writing-plans. Treat the issue body as the starting requirements.
4. **Wait for explicit approval of the plan.** Do not write code until the user approves.
5. **REQUIRED SUB-SKILL:** superpowers:executing-plans, or superpowers:subagent-driven-development when the plan's tasks are independent.
6. **REQUIRED SUB-SKILL:** superpowers:requesting-code-review, using the reviewer agent from Parameters.
7. Run the test command. **REQUIRED SUB-SKILL:** superpowers:verification-before-completion.
8. **Update project records** if the project keeps them: mark the work done and set the next step in the progress file; add or update entries in the test-case doc for new behavior.
9. **REQUIRED SUB-SKILL:** superpowers:finishing-a-development-branch. Open a PR with `gh pr create` that references the issue, or close the issue with a completion comment if the user prefers no PR.

## Rules

- Implement only the approved scope. New scope found mid-way goes back to the user, not into the branch.
- If the issue leaves a product decision open, ask before planning around a guess.
