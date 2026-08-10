# 0008. Forbidden lifecycle operations are severed by a fail-closed runtime guard

- Status: Accepted. The package is merged and tested in CI. It has no call sites
  yet; those land in M1 and are gated on `ADR-002`.
- Date: 2026-08-10 02:10:32 UTC
- Sources: `internal/cnpatroni/guard/guard.go` and `guard_test.go`;
  `hack/cnpatroni/audit/policy/authority-rules.yaml` (`guard_symbol`, rule
  `guard.call`);
  `hack/cnpatroni/upstream/boundary.yaml` (rule `owned.runtime-guard`);
  `.github/workflows/cnpatroni-authority-audit.yml`

## Context

[0007](0007-static-authority-audit-enforces-rule-1.md) can tell you that a
forbidden call exists in the tree. It cannot stop one from executing, and it
cannot see through reflection or through an interface whose implementation is
chosen at run time.

The inherited functions that implement the forbidden operations are real, live
Go code today. When the M1 surgery severs them, the question is what each
severed function does instead. Returning `nil`, or silently doing nothing, turns
a forbidden operation into a caller that believes it succeeded.

## Decision

`internal/cnpatroni/guard` provides one function. Each severed operation returns
`guard.Forbidden(<its own fully qualified symbol>)`, which returns an error
wrapping the package-level `ErrForbidden`, so callers and tests can match it
with `errors.Is`.

The properties that make it a guard rather than a comment, all stated in
`guard.go`:

- **It is unconditional.** "There is no build tag, environment variable, or
  operator setting that disarms it, because CloudNativePatroni has no supported
  mode in which the CloudNativePG high-availability paths may run."
- **It panics under `go test`, and only returns the error in a production
  build.** The panic makes an accidental call in a unit test fail with a stack
  trace "instead of flowing into an error branch that the test happens to
  tolerate". Production does not panic because that "would crash an operator
  process that manages every cluster in its namespace over a defect in one code
  path". The switch is `testing.Testing()`.
- **The operation name is passed as a string literal, not derived from
  `runtime.Caller`.** The reason is that the audit has to verify the wiring
  without executing anything: "a string literal is visible to go/ast, a stack
  frame is not". The audit cross-checks that the literal equals the enclosing
  symbol, which removes the typo risk that would otherwise argue for deriving
  it. The literal is written exactly as it appears in
  `hack/cnpatroni/audit/policy/authority-classification.yaml`.
- **An empty `op` still produces a named error** rather than a blank one.
- **The error text points at the policy file and at specification section 7.4**,
  so a reader who hits it in a log has somewhere to go.

**The package ships before its call sites, deliberately.** `guard.go`: it ships
first "so that the authority audit in `hack/cnpatroni/audit` can verify each
classification marked \"guard: required\" against a call site that already
exists, rather than the audit and the guard arriving in the same change with
nothing checking either."

## Consequences

The M1 severing is one line per forbidden primitive, in the shape `guard.go`
documents:

```go
func (instance *Instance) PromoteAndWait(_ context.Context) error {
	return guard.Forbidden(
		"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres.(*Instance).PromoteAndWait")
}
```

That shape matters for [0006](0006-boundary-manifest-gates-upstream-merges.md):
the audit found that the forbidden primitives are reached through few entry
points — promotion has exactly one caller, the postmaster exec has exactly one
caller — so M1 has "a small number of cut points rather than a diffuse edit,
which is what makes the runtime guard a practical mechanism rather than a
gesture" (`authority-audit.md`). Small local cuts inside a hot file conflict far
less often on an upstream merge than a reorganisation of the same file.

Callers must return the error and must not ignore it (`guard.go`). Every severed
function therefore has to have an error in its signature, or the M1 change has
to give it one.

At M0 nothing in this repository calls the package, and no CloudNativePG code
has been modified. The audit rule `guard.call` (severity `inventory`,
specification section 9.3) consequently reports 0 hits in `authority-audit.md`,
and that zero is the expected value until M1, not a failure.

The package is declared `cnpatroni-owned` in `boundary.yaml`
(`owned.runtime-guard`), so an upstream path arriving at
`internal/cnpatroni/guard/**` is a name collision rather than a merge.

## Enforcement

- `.github/workflows/cnpatroni-authority-audit.yml`, step "Test the runtime
  lifecycle guard": `go test ./internal/cnpatroni/guard/...`.
- `hack/cnpatroni/audit/policy/authority-rules.yaml` declares
  `guard_symbol: .../internal/cnpatroni/guard.Forbidden` and the `guard.call`
  rule, which is how the audit will recognise a wired call site.

## Known gaps

- **The guard is not the proof.** It severs a named symbol; it says nothing
  about a path that never reaches that symbol. The proof is the M3 chaos suite,
  which does not exist (`authority-audit.md`, "Known limitations").
- **No call site exists.** The severing lands in M1 "and only after ADR-002 is
  accepted (specification section 21)" (`guard.go`). `ADR-002` is not written;
  see the reservation in the [index](README.md).
- **`testing.Testing()` distinguishes a test binary, not a test.** A production
  binary invoked from a test harness that is not `go test` gets the
  error-returning behaviour. That is the documented intent, but it is worth
  knowing before relying on the panic as a safety net.
