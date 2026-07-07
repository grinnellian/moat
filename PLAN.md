# Podman support — working plan

Fork-meta document; lives on the fork's `main`, **not** part of the upstream PR
(feature branches for upstream cut from `upstream/main` and don't include it).
Mission context: [ai-lindale#95](https://github.com/grinnellian/ai-lindale/issues/95)
(FEAT-015) — Lindalë wants podman as its default container engine; moat doesn't
speak podman. Status below is updated as work lands.

## Team shape

Architect session (Fable) owns verification, design, and review; implementation
steps are delegated to per-step Sonnet subagents with tight specs. (Operator
override of the seeding session's "start solo" recommendation.)

## Phases

### Phase 1 — socket-emulation spike (empirical, before any code)

Question: does moat's `docker` runtime work unmodified against podman machine's
Docker-API socket via `DOCKER_HOST`?

Verified so far (2026-07-06, macOS arm64, podman 6.0.0, applehv/vfkit VM):

- Podman's socket answers the Docker API (`/_ping` → OK, ApiVersion 1.44).
- moat's Docker client is built with `client.FromEnv` (`internal/container/docker.go`),
  so `DOCKER_HOST` is honored end to end.
- `moat doctor` with `DOCKER_HOST` pointed at the podman socket lists `docker`
  as available.
- ⚠ doctor also reports "gVisor: ✓ available" — podman's compat `/info` lists
  `runsc` from containers.conf even when the binary isn't installed. On Linux,
  moat defaults `sandbox=true` and would pass `--runtime runsc` to a podman
  that can't honor it. Needs a real probe or documented `--no-sandbox` guidance.
- ✅ Full `moat run --runtime docker -- <cmd>` **works unmodified**: pulled
  ubuntu:22.04 through the VM, created/started/streamed/exited 0; container
  env shows `container=podman`. (No grants involved — plain command path.)
- `--grant github` hard-floor run: **blocked on operator** — moat's stored
  github grant fails with "encryption key changed", and re-granting requires a
  valid token (moat validates at grant time; a fake PAT is rejected with 401).
  Same root cause as the invalid gh CLI token. Once `gh auth login` +
  `moat grant github` are re-run, execute Phase 4 below.
- Engine discriminator verified: podman's compat `/version` lists a Component
  named `Podman Engine` — that's the detection hook for doctor/helpers.
- gVisor false positive verified: podman's compat `/info` `Runtimes` lists
  `runsc` (plus kata, krun, youki, …) straight from containers.conf even
  though none are installed. `moat doctor` already misreports
  "gVisor: ✓ available" against podman on this machine.

Known risk areas being exercised: derived-image build path (`moat/run:<hash>`),
`--add-host … host-gateway` sentinel (podman ≥4.1 supports it), TLS-intercepting
proxy reachability from the VM, rootless UID mapping vs. moat's root-user
base-image contract.

### Phase 2 — design (architect) — DECIDED

Phase 1 verdict: emulation works, so podman support = detection + doctor +
docs + a thin `--runtime podman` alias. Decisions:

1. **No new `RuntimeType`.** `--runtime podman` / `MOAT_RUNTIME=podman`
   resolves to the existing `DockerRuntime` pointed at a podman socket;
   `Type()` stays `docker` so every existing switch remains valid.
2. **Auto-detection** via the existing `alternativeDockerSockets()` precedent
   (Rancher Desktop) in `internal/container/detect.go`: append podman machine
   API sockets on macOS (`$TMPDIR/podman/*-api.sock`) and, on Linux,
   `$XDG_RUNTIME_DIR/podman/podman.sock` (rootless) + `/run/podman/podman.sock`
   (rootful).
3. **Engine identification**: `/version` Components contains "Podman Engine";
   cached helper on `DockerRuntime` (mirrors the gvisor sync.Once pattern).
4. **Doctor**: show the engine behind the docker socket
   ("docker (Podman Engine)") and annotate the gVisor line as
   engine-reported/unverified when the engine is podman (see false positive
   above). No behavior change to sandbox selection in v1 — documented instead.
5. **Docs**: podman page in getting-started (machine setup incl. vfkit/krunkit
   provider note, DOCKER_HOST, Linux podman.socket, rootless + root-user
   base-image note, gVisor caveat); fix comparison table.

Commit layout keeps each piece droppable by upstream: detection, doctor, flag
alias, docs as separate conventional commits on `feat/podman` (cut from
`upstream/main`, no PLAN.md).

### Phase 3 — implement (sonnet, per-step specs)

Small conventional commits, mergeable with upstream, no drive-by refactors.
Steps written after Phase 2; each step gets its own spec + review by architect.

### Phase 4 — verify the hard floor

`moat run --grant github` under podman: container env contains only moat
placeholder values (`ghp_moatProxyInjectedPlaceholder…`) and `gh api user`
succeeds via proxy injection.

### Phase 5 — upstream

PR against majorcontext/moat (no existing podman issues/PRs as of 2026-07-06 —
verified via search, clear field). If upstream declines or goes quiet, document
this fork as Lindalë's pinned source with the delta isolated.

## Current blockers

- `gh` auth token on this machine went invalid (was valid earlier 2026-07-06).
  Blocks: creating the fork, pushing, posting to ai-lindale#95, opening the PR.
  Operator action: `gh auth login -h github.com`.

## Environment notes (this machine)

- Podman machine: **applehv/vfkit provider**, not the libkrun default —
  podman 6.0.0's libkrun path needs `krunkit`, which isn't installed; vfkit
  ships inside the Homebrew podman formula. Recreate with
  `CONTAINERS_MACHINE_PROVIDER=applehv podman machine init && … start`.
- Keep dev tools off the host (operator preference): toolchains run in
  containers; host already had Go 1.26.2 and the moat dev binary
  (`~/go/bin/moat` — don't clobber; build the fork to `./bin/`).
- Docker Desktop's daemon hangs on all registry pulls on this machine; podman
  is the working pull path.
