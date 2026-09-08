# Development

## Requirements

Go, Node 22 and Docker. The Go toolchain version is pinned in
`backend/go.mod`.

## Running the parts separately

```sh
make run-backend    # Go server with backend/.env loaded
make run-frontend   # SvelteKit dev server with hot reload
make local          # the whole stack in Docker, as on the server
```

## Tests

```sh
make test           # svelte-check and go test ./...
make lint           # prettier, eslint, gofmt and go vet
make e2e-install    # once: Playwright and its Chromium build
make e2e            # end-to-end suite
```

`make e2e` builds the production image and drives the whole application
through a browser: registration, all three kinds of invite, availability and
notification settings, clubs, and a slot poll from creation to a booked
winner. It runs against a throwaway database, the built-in mock scrape source
and a [Mailpit](https://mailpit.axllent.org) inbox that catches every outgoing
mail. See [e2e/README.md](../e2e/README.md).

## Continuous integration

`.github/workflows/ci.yml` runs on every push and pull request with three
jobs: frontend lint, type check and build; backend format, vet, golangci-lint
and tests; and the end-to-end suite.

golangci-lint runs with `only-new-issues`, so existing findings do not block a
pull request while new code has to be clean.
