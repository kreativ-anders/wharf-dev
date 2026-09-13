# Contributing

[CLAUDE.md](CLAUDE.md) is the working agreement — for people as much as for
AI tools — and the only copy of the rules. Read it before a non-trivial
change; this file only says how to get going.

## Set up

- Go, at the version in [`daemon/go.mod`](daemon/go.mod)
- Flutter stable, at the version pinned in
  [`.github/workflows/release.yml`](.github/workflows/release.yml)

```sh
make gui      # build everything, launch the app against a throwaway root
make check    # everything a release is gated on
```

## Workflow

1. **Spec first.** New or changed behaviour starts as a scenario in
   [`features/`](features/); `make spec` shows it as `✗ NO TEST` until a test
   claims it (CLAUDE.md §1).
2. Implement, then claim the scenario with a comment above the test.
3. `make fmt vet test` — plus `make race` for the supervisor or IPC, and
   `make gui-test` for the GUI.

## Commits

One change per commit. The subject is a [gitmoji](https://gitmoji.dev)
followed by an imperative English sentence — `🐛 Keep the front door up when
one project fails`. Subjects go into [CHANGELOG.md](CHANGELOG.md) unchanged
when a release is cut, so write them for someone reading release notes.
The body says *why*.

A change to behaviour carries its `.feature` edit in the same commit, never in
a follow-up.

## Releases

Maintainers only — see [dev/releasing.md](dev/releasing.md).
