# AI Agent Directives

This file contains standing instructions for AI coding agents working in this
repository.

---

## Definition of done

**A change is not complete until `task check` passes without errors.**

- Run `task check` after every set of edits and fix any failures before declaring the work done.
- Do not reduce the scope or strictness of `task check` (e.g. removing lint rules, lowering the coverage threshold, skipping steps) without explicit human consent.

---

## Changelog maintenance

**Maintain `CHANGELOG.md` for every end-user facing change.**

- All changes go under `## [Unreleased]` until a version tag is cut.
- Use Keep a Changelog subsection headings: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`.
- One bullet per feature/fix, one line. State what changed and what it means to
  the operator; omit implementation detail.
- **Before writing or keeping any bullet, apply this litmus test:**
  *"Could an operator observe or act on this without reading the source code?"*
  If no, drop it.
  - ✅ **Keep** (operator-observable): exposed endpoints, HTTP status codes returned,
    config keys / env vars, CLI flags, error messages, default values that affect
    behaviour, headers injected into requests the operator can inspect.
  - ❌ **Drop** (implementation detail): internal function calls, cache TTLs,
    upstream URL paths chosen by the code, internal header-formatting schemes,
    retry heuristics based on response body content, internal protocol negotiation,
    plugin manifest fields, or any other mechanism the operator never touches.
- **Breaking changes** get `**BREAKING:**` in the bullet and a `### Breaking Changes`
  summary at the top of the version section.
- Internal-only changes (refactors, tests, CI, doc-only) do **not** need an entry.
- On release: move `[Unreleased]` entries into a dated version section and update
  the comparison links.
