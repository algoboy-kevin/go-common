# go-common

Small, reusable Go packages shared across projects: helpers, data-handling
building blocks and venue-neutral market logic. One module, many independent
packages. An application imports only the packages it uses.

```
go get github.com/algoboy-kevin/go-common@latest
```

## Packages

| Package | What it is | Dependencies |
|---|---|---|
| _(none yet; see "Admission" below)_ | | |

Add one row per package when it lands: name, one line, and its non-stdlib
dependencies (or "stdlib").

## Rules

**Admission.** Code enters only when two projects already use it or when the
same problem has come up twice. No speculative helpers.

**Dependencies.** Every package is stdlib-only unless it sits under a clearly
named group whose directory name states the dependency (for example `store/`
for parquet, `ship/` for cloud storage). Importing one package must never pull
another package's third-party dependencies. If a group's dependencies become a
burden for a consumer, the group moves to its own module without changing code.

**Direction.** This module sits at the bottom of the dependency graph. It
never imports an application or an exchange client.

**Quality bar.** Every package has a package doc comment, table-driven tests
that run in well under a second, and no global mutable state. Hot-path types
document whether they allocate.

**Stability.** Tag releases (`vX.Y.Z`). Before v1, breaking changes bump the
minor version and are listed in the tag notes.

## Using it locally while developing

Prefer a `go.work` file next to the consumers over `replace` lines in
`go.mod`:

```
go work init ./app ../go-common
```

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
