# Quality audit (a recurring task)

An on-demand review, asked for by a person, typically before a release or
when it has not run in a while. There is deliberately no CI step for it: its
point is judgement, and a checker that cries wolf gets ignored.

## Scope

1. **Usability and accessibility of the app** — `gui/lib/`. Contrast (WCAG
   AA, enforced for the palette by `gui/test/theme_test.dart`), status never
   by colour alone, every control labelled with its target, keyboard reach,
   focus order, screen-reader semantics, and consistent interaction patterns
   (`dev/design-principles.md` §2 and §4).
2. **The landing page** — `docs/`. Title and meta, structured data (the FAQ
   JSON-LD is already guarded by `internal/docsync`), `robots.txt` and
   `sitemap.xml`, Open Graph, heading order, link targets, asset sizes, and
   whether every promise on the page still matches `features/`.
3. **Code, without changing behaviour** — `daemon/` and `gui/`. Dead code,
   duplication, missing error handling at the edges (processes, files,
   network), error messages that do not name the fix (CLAUDE.md §4), and
   comments that explain *how Wharf works* rather than *why this line*.

## Rules

- `make check` stays green, and nothing listed under "Settled decisions" in
  CLAUDE.md §3 changes.
- **Fix directly** what changes no visible behaviour and needs no decision:
  typos, dead links, missing labels or meta tags, an established contrast fix
  applied consistently, refactors that change no behaviour.
- **Ask first** about anything that changes what a user sees or does, a
  wording or design choice, or anything that needs content or credentials only
  a person has.
- Report in the conversation, not in a file: what was fixed (with
  `file:line`), what is open and why. This document is the only lasting trace
  of the audit itself; its findings live in the commits.
- A finding that is a spec gap follows the sync rule (CLAUDE.md §1) like any
  other change.
