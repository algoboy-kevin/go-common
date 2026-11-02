# Contributing to go-common

## Before writing code in any project

Search this repo first (`README.md` package table, then `grep` the packages). If
something close exists, use it or extend it. Do not write a second copy.

## When to add a package

Add (or move in) code when at least one of these is true:

- two projects already contain the same or near-identical code;
- you are about to write a helper you have written before;
- a piece of logic is venue-neutral or project-neutral and a second project
  will clearly need it.

If none is true, keep it in the project and revisit when a second use appears.

## How to add a package

1. One concern per package, named for what it does (`ring`, `retry`,
   `atomicfile`), not for who uses it. No `util` or `helpers` packages.
2. Stdlib only, unless the directory name states the dependency (`store/`,
   `ship/`).
3. Package doc comment, exported identifiers documented.
4. Table-driven tests, fast (a whole package well under a second). Slow or
   networked tests do not belong here.
5. No imports of applications or exchange clients.
6. Add a row to the package table in `README.md`.
7. When moving code out of a project, move its tests with it, then switch the
   project to import the package in the same change set, so no copy remains.

## Moving code out of a project: checklist

- [ ] The package builds and its tests pass here with no project imports.
- [ ] Behaviour is unchanged: the project's own tests still pass against the
      imported package.
- [ ] The project's copy is deleted, not left alongside.
- [ ] README table updated; a tag is cut when a project needs the new version.

## Commits and tags

Small commits, imperative subject lines. Tag `vX.Y.Z` when a consumer needs a
version; before v1, breaking changes bump the minor version.
