---
name: product-dev
description: Use when the user has a rough product or feature idea and wants it planned or designed before any code exists, says "plan/design a feature" or "plan/design a product", runs /product-dev, or asks for a PRD, ADR, or functional spec.
---

# Product development flow

Take an idea from one sentence to a buildable, documented plan **without the user writing code**. The user is the product owner; you do discovery, design, and documentation, then hand structured work to the discipline skills. This skill operationalizes the Product Documentation Playbook in [references/playbook.md](references/playbook.md) — read it for templates, examples, and the full reasoning.

superpowers:brainstorming covers intent discovery; this skill adds right-sizing and docs governance, and runs before superpowers:writing-plans.

## Parameters

Read these from the project's `AGENTS.md` (or a `## Parameters` block it points to). Fallback when absent:

| Parameter | Fallback |
|---|---|
| PRD dir | `docs/prd/` |
| ADR dir | `docs/adr/`, files numbered `NNNN-<slug>.md` |
| Functional spec dir | `docs/functional_spec/` |
| Reference docs (architecture, data-model, api) | `docs/reference/architecture.md`, `docs/reference/data-model.md`, `docs/reference/api.md` |
| Discipline skills to route tasks to | whatever stack skills are installed in the project; if none, name the stack in each task |

If a directory has a `README.md` index, add new docs to it. If the project has a docs hub, link new docs from it.

## Golden rule — interview BEFORE writing
An AI that writes a full PRD from "I want X" is inventing the product. Extract decisions from the user first.
- **Challenge vague answers — your job is to sharpen, not to please.** "Users" / "everyone" is not a user. Push until specific.
- **If the idea is a category** ("an e-learning system", "an HR tool") **not a product, say so and make them pick a slice.**
- Ask in small batches (≤4 questions), but when an answer is fuzzy, drill into that one concept before moving on. Recommend a default for every decision.
- Flag any section where you had to guess so the user can correct it.

`AskUserQuestion` is Claude Code-specific. In other agents (Codex, OpenCode, Cursor) ask the same batched questions in plain chat, each with its recommended default.

## Right-size first (don't over-document)
Before running the full flow, classify the work and produce only the **lightest sufficient** artifact. State which bucket you picked and why before writing anything.
- **New product / new audience / new business model** → **full flow** (PRD → architecture → ADRs → specs).
- **A feature inside an existing product that adds user-facing behavior** (e.g. export a report as CSV, add search to the orders list) → **skip the PRD; write just a functional spec** (§4), plus an ADR only if it forces a hard, irreversible choice. First confirm it serves the existing PRD's core workflow — if not, it may be out of scope.
- **A technical change with no user-facing behavior** (e.g. swap a UI component library, replace a database driver, refactor a module) → **no PRD/spec.** Record one ADR if the decision is significant and hard to reverse; otherwise hand it straight to the discipline skill.
- **Trivial** (copy, bugfix, rename) → just do it.

A one-page PRD for a small feature is a smell. When unsure between two buckets, pick the lighter one and say so. The user can always ask for more.

## Order of creation (when the full flow applies)
PRD → Architecture overview → ADRs (as decisions arise) → Functional specs (one per story, just-in-time). Stay in **plan mode** until artifacts are approved; present the file list before writing.

## 1. PRD → `<PRD dir>/<slug>.md`
One page. Interview to fill, in the user's words:
- **Problem** — who has what pain today, why existing solutions fail (specific enough a stranger could identify the user).
- **Target user** — narrow: demographics, context, budget, technical level.
- **Core workflow** — the ONE thing v1 must do well. If they list many, make them rank and pick one. *This is the product.*
- **Success metrics** — 2–3 measurable, with numbers + timeframe.
- **Out of scope for v1** — explicit list; confirm each item. *This section does the most work.*
- **Riskiest assumption + cheapest test** — the one belief that, if wrong, kills it; design a test under ~2 weeks / ~$100 / ideally zero code. **Run this test before building.**

Match the existing PRD shape, index it, cross-link related docs.

## 2. Architecture overview → update the reference docs (architecture, data-model, api)
Interview first: team size & known stack, **honest 6-month scale** (push back on "millions"), the entities + rules implied by the core workflow, the 1–3 genuinely risky parts (security, money, untrusted input, external integrations), and what they're tempted to build that v1 doesn't need.
Apply these **defaults unless overridden**: modular monolith over microservices · one boring relational DB (Postgres) · the stack the team already knows · buy/managed services for anything non-core · simplest deploy with one-command deploy + rollback.
- Mark every **expensive-to-reverse** choice "→ needs an ADR".
- Keep it tight (<5 pages of change); update the data-model and api docs for new entities/endpoints. Spend the most care on the **data model** — it's the costliest thing to change later.

## 3. ADRs → `<ADR dir>/NNNN-<slug>.md` (immutable)
For each flagged decision act as a sparring partner: lay out 2–4 realistic options with **cost to build · cost to operate · risk · reversibility** (no strawmen); recommend one *and* give the strongest honest case for the runner-up; let the user choose before writing. The ADR (Title · Status · Date · Context · Options · Decision · Consequences) must include **≥2 genuine downsides of the chosen option**. Index it.
**If the decision is easily reversible, say it doesn't deserve an ADR — pick one and move on.**

## 4. Functional specs → `<functional spec dir>/NN-<slug>.md` (just-in-time, one per story)
Write the day before building the story, not all up front. Process: have the user describe the happy path → reflect as numbered steps → **hunt edge cases step by step** ("what can go wrong here?": network, invalid input, concurrency, malicious users, empty states, limits) and define a behavior for each → pin every number/limit/threshold exactly → write **Given/When/Then** acceptance criteria where **every business rule maps to ≥1 criterion**.
- **No "TBD"** — force a decision or move it to "out of scope for this story".
- The story must be **buildable in 1–3 days**; if it grows past 1–2 pages, it's too big — propose a split.
Index it.

## 5. Implementation plan
Tasked checklist grouped by area (e.g. backend, API, web, mobile), each task pointing at the discipline skill that does it (see Parameters). Each task names its acceptance criteria and required tests. Hand the plan to superpowers:writing-plans when the project uses it.

## Which doc answers what (routing)
Feature request? → check the **PRD** (not serving the core workflow = out of scope). "Where does this live?" → **architecture**. "Why did we do it this way?" → **ADR**. "What exactly happens when…?" → the story's **functional spec**.

## Definition of done for a planning pass
PRD (with riskiest-assumption test) written + indexed · architecture/data/api updated if affected · ADR(s) for the hard choices with honest downsides · functional spec(s) with testable, no-TBD criteria · a tasked build plan · every cross-link resolves. Confirm before implementation begins.
