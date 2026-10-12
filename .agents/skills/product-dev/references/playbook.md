# Product Documentation Playbook
### Four documents, four AI prompts — from idea to buildable spec

This playbook gives you, for each document type:
1. **Purpose** — what question it answers and who reads it
2. **Template** — the structure to fill in
3. **Example** — a snippet from a sample product ("CodePath", an e-learning platform for Indonesian self-taught developers)
4. **AI prompt** — paste this into your AI assistant; it will interview you, then draft the doc

**The golden rule for every prompt:** the AI must interview you BEFORE writing. An AI that writes a full PRD from "I want an e-learning system" is inventing your product for you. The prompts below force it to extract decisions from you first.

**Order of creation:**
PRD → Architecture overview → ADRs (as decisions arise) → Functional specs (one per story, just-in-time)

---

## 1. PRD — Product Requirements Document

**Answers:** Why does this product exist, for whom, and what must v1 do?
**Audience:** Everyone (founders, engineers, designers, future hires)
**Length:** One page
**Lifespan:** Written first; rarely changes; the north star for scope decisions

### Template

```
# [Product name] — PRD v1

## Problem
Who has what pain, today, and why are existing solutions failing them?
(2–4 sentences, specific enough that a stranger could identify the user)

## Target user
Demographics, context, budget, technical level. Be narrow — "everyone" is no one.

## Core workflow (the ONE thing v1 must do well)
A single sentence or short flow. This is the product.

## Success metrics
2–3 measurable outcomes with numbers and timeframes.

## Out of scope for v1
Explicit list. This section does the most work in the document.

## Riskiest assumption
The single belief that, if wrong, kills the product.
How will we test it cheaply, before building?
```

### Example (CodePath)

```
## Problem
Self-taught developers in Indonesia learn from YouTube but get stuck:
no structured path, no verification they understood anything. They watch
passively and cannot build afterward.

## Target user
Self-taught developers, 18–28, 0–2 yrs experience, learning in Bahasa
Indonesia, budget ~Rp 100k/month (cannot afford Rp 20jt bootcamps).

## Core workflow
Learner picks a track → completes a short lesson → must pass an
auto-graded hands-on coding exercise before the next lesson unlocks.
The product is the gated, verified progression — not the videos.

## Success metrics
- 40% of users who finish lesson 1 finish lesson 5 within 2 weeks
- 10% free→paid conversion within 30 days

## Out of scope for v1
Instructor marketplace, live classes, certificates, mobile app, forums,
multiple languages, admin dashboards.

## Riskiest assumption
Learners want verified skill progression more than certificates.
Test: manually grade exercises for 5 learners over WhatsApp for 2 weeks
before building any grading engine.
```

### AI prompt — PRD interviewer

```
You are a senior product manager helping me write a one-page PRD.
I will give you a raw product idea. Do NOT write the PRD yet.

First, interview me. Ask me questions ONE AT A TIME (wait for my answer
before the next question) until you can fill every section below with
MY decisions, not your inventions:

1. Problem: who exactly has this pain? How do they solve it today?
   Why does that fail? Push me if I'm vague — "students" is not an answer.
2. Target user: demographics, budget, technical level, context.
   Force me to narrow until the user is specific.
3. Core workflow: what is the ONE thing v1 must do well? If I list
   multiple features, make me rank them and pick one.
4. Success metrics: propose 2–3 measurable metrics and let me adjust.
5. Out of scope: list everything I mentioned that is NOT the core
   workflow and ask me to confirm each item as out of scope for v1.
6. Riskiest assumption: propose what you think the riskiest assumption
   is, ask if I agree, then help me design the cheapest possible test
   (under 2 weeks, under $100, ideally zero code).

Rules:
- Challenge vague answers. Your job is to sharpen, not to please.
- If my idea is a category ("e-learning") not a product, say so and
  make me pick a slice.
- Only after the interview, write the PRD on ONE page using my answers.
- Flag any section where you had to guess, so I can correct it.

My idea: [PASTE YOUR IDEA AND REASONING HERE]
```

---

## 2. Architecture overview

**Answers:** How is the system shaped? What are the load-bearing walls?
**Audience:** Engineers (current and future)
**Length:** 3–5 pages
**Lifespan:** Written before the walking skeleton; updated when structure actually changes

### Template

```
# [Product name] — Architecture overview

## System context
What external actors and systems does our product talk to?
(users, payment provider, email service, code sandbox, etc.)

## Major components
The 3–7 big pieces and their responsibilities. For a v1 this is often:
web app, API/monolith, database, background workers, 1–2 external services.

## Core data model
The main entities, their key fields, and relationships.
This is the most expensive thing to change later — spend time here.

## Critical flows
The 1–3 flows where the design is non-obvious or risky.
Step-by-step: what calls what, what is stored, what can fail.

## Cross-cutting decisions
Auth approach, hosting/deployment, observability (errors + analytics),
environment strategy (local / staging / prod).

## What we are deliberately NOT designing yet
Mirror of the PRD's out-of-scope list, in technical terms.
```

### Example (CodePath — core data model excerpt)

```
User (id, email, locale, plan)
Track (id, title, slug, published)
Lesson (id, track_id, order, title, content_md, video_url)
Exercise (id, lesson_id, starter_code, test_suite, language, time_limit)
Submission (id, user_id, exercise_id, code, status[queued|pass|fail|error],
            feedback, created_at)
Progress (user_id, lesson_id, state[locked|available|completed])

Rule encoded in data: Lesson N+1's Progress flips locked→available only
when a Submission with status=pass exists for every Exercise of Lesson N.

Critical flow — code submission:
1. POST /submissions → row created (status=queued) → job enqueued
2. Worker sends code + test_suite to sandbox service (isolated, 5s limit)
3. Result written to Submission; if pass → Progress recalculated
4. Client polls /submissions/:id (v1: polling; websockets out of scope)
Failure paths: sandbox timeout → status=error, attempt not counted.
```

### AI prompt — architecture interviewer

```
You are a pragmatic staff engineer helping me write an architecture
overview for a v1 product. Here is my PRD: [PASTE PRD]

Do NOT design the system yet. First interview me, one question at a time:

1. Team & constraints: how many engineers, what stack do we already
   know well, what is the budget for infrastructure?
2. Scale honesty: how many users in the first 6 months, realistically?
   (Push back if I say millions.)
3. For each entity implied by the PRD's core workflow, ask me about
   the relationships and rules I expect.
4. Identify the 1–3 genuinely risky technical parts (security,
   external integrations, anything involving money or untrusted input)
   and go deeper only on those.
5. Ask what I am tempted to build that v1 does not need, and challenge
   me to defer it.

Then write the overview using these defaults unless I overrode them:
- Modular monolith over microservices
- One boring relational database (Postgres)
- The stack the team already knows
- Buy/use managed services for anything not core to the product
- Simplest deployment that gives one-command deploys + rollback

Rules:
- Every choice that is expensive to reverse must be marked
  "→ needs an ADR".
- Keep it under 5 pages. If a section needs more, it is probably
  a functional spec or an ADR, not architecture.
- End with the "NOT designing yet" list.
```

---

## 3. ADR — Architecture Decision Record

**Answers:** Why did we choose X over Y, and what did we accept by doing so?
**Audience:** Engineers, especially future ones (including future you)
**Length:** Under one page each
**Lifespan:** Append-only. Never edited — superseded by a new ADR if the decision changes.

### Template

```
# ADR-NNN: [Decision in one sentence]
Date: YYYY-MM-DD
Status: accepted | superseded by ADR-MMM

## Context
What problem forced a decision? What constraints applied?

## Options considered
2–4 options, one line each, with the main pro/con.

## Decision
What we chose, in one or two sentences.

## Consequences
What we gain. What we accept as a downside. What this makes
harder later. (Honest downsides are the whole point.)
```

### Example (CodePath)

```
# ADR-003: Execute learner code in a managed sandbox API, not self-hosted
Date: 2026-06-20
Status: accepted

## Context
Learners submit untrusted code that we must run. Self-hosting execution
(Docker/Firecracker) is a security-critical, ops-heavy system. We are
2 engineers and code execution is necessary but not our differentiator.

## Options considered
- Self-hosted Docker runners: full control; large security/ops burden
- Firecracker microVMs: best isolation; high build cost for 2 people
- Managed sandbox API (e.g. Judge0 or similar): fast to ship;
  per-execution cost; vendor dependency

## Decision
Use a managed sandbox API behind our own SubmissionRunner interface.

## Consequences
+ Shipping grading in days, not months; security burden offloaded
- Cost scales per submission (~acceptable below 50k subs/month)
- Vendor lock-in risk, mitigated by the SubmissionRunner abstraction
  so we can swap to self-hosted later if volume justifies it.
```

### AI prompt — ADR sparring partner

```
You are a staff engineer acting as my sparring partner for an
architecture decision. I need to produce an ADR.

The decision I'm facing: [DESCRIBE THE DECISION]
Context (team size, stack, scale, budget): [CONTEXT]

Process:
1. Restate the decision and ask clarifying questions about constraints
   I did not mention (one at a time, max 4 questions).
2. Lay out 2–4 realistic options. For each: cost to build, cost to
   operate, risk, and reversibility. No strawmen.
3. Recommend one option and argue for it — but also give the strongest
   honest argument for the runner-up.
4. Ask me to decide. Do not write the ADR until I have chosen.
5. Write the ADR in under one page, with a Consequences section that
   includes at least 2 genuine downsides of MY chosen option.

Rule: if my decision is easily reversible, tell me it does not deserve
an ADR and that I should just pick one and move on.
```

---

## 4. Functional spec (story-level)

**Answers:** Exactly how does this feature behave, including edge cases?
**Audience:** Whoever builds and tests the story
**Length:** 1–2 pages per story
**Lifespan:** Written JUST-IN-TIME, right before building the story. After shipping, it becomes the permanent record of behavior.

### Template

```
# Spec: [Story title, as a user outcome]
Epic: [parent epic]   PRD workflow: [which part of the core flow]

## User story
As a [user], I want [action], so that [outcome].

## Flow (happy path)
Numbered steps: what the user does, what the system shows/does.

## Business rules
Numbered, testable rules. Numbers, limits, thresholds — be exact.

## Edge cases & failure states
What happens when things go wrong or users do the unexpected.
Each one gets a defined behavior — "TBD" is not a behavior.

## Acceptance criteria
Given / When / Then format. If you cannot write it this way,
the requirement is not specific enough yet.

## Out of scope for this story
What the next story will handle instead.
```

### Example (CodePath)

```
# Spec: Learner submits code and sees the result
Epic: Exercise grading   PRD workflow: gated progression

## Business rules
1. Max 3 attempts per exercise per 30-minute window; cooldown shown
   as a countdown timer.
2. Execution time limit: 5 seconds; exceeding it → status "error",
   does NOT consume an attempt.
3. A "pass" requires all test cases green; partial passes show
   which tests failed (name + expected vs actual), never the test code.
4. Passing any exercise is permanent — later failed attempts cannot
   re-lock progress.

## Edge cases
- Sandbox unreachable → "Grading temporarily unavailable", attempt
  not counted, error logged to Sentry with submission id.
- User submits identical code twice in a row → second submission
  rejected client-side with a hint to change something.
- User closes the tab while queued → result persists; shown on return.

## Acceptance criteria
- Given a learner with 0 attempts, When their code passes all tests,
  Then status shows "Passed" and the next lesson unlocks within 5s.
- Given a learner with 3 failed attempts in 30 minutes, When they
  try again, Then submission is blocked and a countdown is shown.
```

### AI prompt — spec writer & edge-case hunter

```
You are a senior engineer + QA lead hybrid. Help me write a functional
spec for ONE story. Context: [PASTE PRD core workflow + the story title]

Process:
1. Ask me to describe the happy path in my own words.
2. Reflect it back as numbered steps; ask me to correct.
3. Hunt edge cases: walk through the flow step by step and for each
   step ask "what can go wrong here?" — network failures, invalid
   input, concurrent actions, malicious users, empty states, limits.
   Propose a behavior for each and let me confirm or change it.
4. Extract business rules: every time I say something with a number
   or condition, pin it down exactly (how many, how long, what then).
5. Write acceptance criteria in Given/When/Then. Every business rule
   must map to at least one criterion.

Rules:
- No "TBD" allowed in the final spec — force me to decide or
  explicitly move it to "out of scope for this story".
- Keep it to 1–2 pages. If it grows beyond that, tell me the story
  is too big and propose how to split it.
- This story must be buildable in 1–3 days. Check that with me.
```

---

## How the documents connect

```
PRD ──(constrains)──► Architecture overview ──(justified by)──► ADRs
 │
 └──(core workflow splits into epics → stories)──► Functional specs
                                                    (one per story,
                                                     just-in-time)
```

- A feature request? Check it against the **PRD**. Not serving the core workflow → out of scope.
- A structural question ("where does this live?")? Check the **architecture overview**.
- A "why did we do it this way?" question? Check the **ADRs**.
- A "what exactly should happen when…?" question? Check the **functional spec** for that story.

After 3–6 months of building this way, the four layers together ARE the
single comprehensive guiding document — except every page in it was
written at the moment its contents were actually known, so it is correct.

## The AI workflow, end to end

1. Drop your raw idea into the **PRD prompt** → answer the interview → get a one-page PRD.
2. Run the cheap test of the riskiest assumption (no code).
3. Paste the PRD into the **architecture prompt** → answer the interview → get the overview, with "needs an ADR" flags.
4. For each flagged decision, run the **ADR prompt** → decide → record.
5. Build the walking skeleton.
6. For each story you pick up, run the **spec prompt** the day before you build it.
7. Ship, measure, reorder the backlog, repeat from step 6.
