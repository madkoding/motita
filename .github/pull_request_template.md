<!-- Thanks for sending a PR! Please fill in every section below. -->
<!-- CI already enforces most of this, but the checklist is for YOU to catch
     problems before pushing, not just to wait for CI to fail. -->

## Summary

<!-- What does this PR change, and why? One or two paragraphs. -->

## Related issue

<!-- Fixes #NNN, or "N/A" if there is no issue. -->

## What changed

<!-- Bullet list of the concrete changes. Group by area if the PR is large. -->

-

## Verification

<!-- Check every box that applies. If a box does not apply, explain why. -->

- [ ] `make fmt` — code is gofmt-clean
- [ ] `make vet` — `go vet` passes
- [ ] `make test` — all tests pass
- [ ] `make cover` — **100% coverage** on every package in `./internal/...`, `./cmd/...` and `./tools/...` (test-support packages exempt)
- [ ] `make build` — the binary builds for the host platform
- [ ] `make check` — the full local gate passes (`fmt-check` + `vet` + `test`)

### If you added a new package or file

- [ ] Tests were added alongside the code (100% coverage is the gate)
- [ ] No Spanish in code, configs, or scripts (the language gate in `scripts/verify.sh` will fail otherwise)
- [ ] If the package ships in the binary, it is covered by the coverage gate's package list

### If you changed the TUI or web UI

- [ ] Ran `make run` and manually verified the UI still works
- [ ] Screenshots attached if the visual output changed

### If you changed CI or build

- [ ] Verified in a clean checkout (CI gets a fresh tree without your local state)

## Breaking changes

<!-- "None" or describe what breaks and for whom. -->