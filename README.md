# go-controller-template

A CLASTIX template for building Kubernetes controllers in Go with
[controller-runtime](https://github.com/kubernetes-sigs/controller-runtime),
packaged and shipped with [ko](https://ko.build) and a Helm chart.

Use it as a starting point: clone it, rename the placeholders to your project,
and start adding your reconcilers.

## What you get

- A `main.go` wired with a controller-runtime manager (metrics, health/ready
  probes, leader election).
- Build & release tooling via `ko` and a `Makefile`.
- A Helm chart under `charts/` (Deployment, RBAC, ServiceAccount, leader
  election role).
- RBAC generation from kubebuilder markers (`make rbac`).
- Linting with `golangci-lint` and a SPDX license header check.
- GitHub Actions for CI and release (image + chart published to `ghcr.io`).

## Getting started: rename the template

The template uses two placeholders throughout the repository. Replace both with
your own values:

| Placeholder | Meaning | Replace with |
| --- | --- | --- |
| `go-controller-template` | the project / module / chart name | your project name, e.g. `my-controller` |

### 1. Go module path

**`go.mod`** — line 1:

```
module github.com/clastix/go-controller-template
```

Change to `github.com/clastix/<your-project>`. Then update the import in
**`main.go`**:

```go
"github.com/clastix/go-controller-template/pkg"
```

> Tip: run `go mod edit -module github.com/clastix/<your-project>` and then
> fix the imports, or do a project-wide find & replace.

### 2. main.go

In **`main.go`**, change the leader-election ID so it is unique to your
controller:

```go
LeaderElectionID: "go-controller-template.clastix.io",
```

This is also where you register your reconcilers — see the commented
`Add your controllers logic here:` block.

### 3. Makefile

In **`Makefile`**:

- `CONTAINER_REPOSITORY ?= ghcr.io/clastix/$(MODULE_NAME)` — change `clastix` to
  your registry namespace.
- `KO_LD_FLAGS ?= "-X github.com/clastix/$(MODULE_NAME)/pkg.GitTag=$(VERSION)"`
  — change `clastix` to match your module path.
- The `chart` target pushes to `oci://ghcr.io/clastix/charts` — change to your
  charts registry.

`MODULE_NAME` is derived automatically from the last path segment of your
`go.mod` module, so renaming the module updates most targets for free.

### 4. .ko.yaml

In **`.ko.yaml`**, rename the build `id`:

```yaml
builds:
  - id: go-controller-template
```

### 5. .golangci.yml

In **`.golangci.yml`**:

- `gci.sections.prefix(github.com/clastix/go-controller-template/)` — update to
  your module path so imports are grouped correctly.
- `goheader.template` — change `Clastix Labs` to your own copyright holder. Also
  update the header in `main.go` and `pkg/version.go` to match.

### 6. Helm chart

Rename the chart directory `charts/go-controller-template/` to
`charts/<your-project>/`, then update the contents:

- **`Chart.yaml`** — `name` and `description`.
- **`values.yaml`** — `image.repository` (`ghcr.io/clastix/go-controller-template`).
- **`templates/_helpers.tpl`** — every `go-controller-template.*` template
  name (`.name`, `.fullname`, `.chart`, `.labels`, `.selectorLabels`,
  `.serviceAccountName`).
- **`templates/*.yaml`** — the `include "go-controller-template.*"` references
  and the hardcoded resource names in `rbac.yaml` / `rbac_election.yaml`
  (e.g. `go-controller-template-role`).

> Tip: the template names in `_helpers.tpl` and the `include` calls must match
> exactly, so a directory-wide find & replace of `go-controller-template` is the
> safest approach.

### 7. GitHub Actions

In **`.github/workflows/`** the release workflow logs into `ghcr.io` and pushes
under CLASTIX org automatically (`github.actor` / `GITHUB_TOKEN`), so no edits are usually needed —
but double-check GitHub organisations permissions for packages.

## Quick find & replace

A one-shot starting point (review the diff afterwards):

```sh
grep -rl --exclude-dir=.git -e go-controller-template -e clastix . \
  | xargs sed -i 's/go-controller-template/<your-project>/g'

git mv charts/go-controller-template charts/<your-project>
```

Then update copyright holders (`Clastix Labs`) separately, since you likely want
your own organisation name there rather than `<your-org>`.

## Development

```sh
make help     # list available targets
make lint     # run golangci-lint
make rbac     # regenerate chart RBAC from kubebuilder markers
make build    # build a local image with ko
make push     # build and push the image
make chart    # package and push the Helm chart
```

## License

Apache-2.0. Update the headers and this section to reflect your own project.