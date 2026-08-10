# Security policy

## Supported versions

None. CloudNativePatroni has no releases and no supported versions. It is a
pre-alpha architecture spike and must not be deployed. Nothing in this
repository is maintained against a security response commitment.

## Reporting a vulnerability

There is no vulnerability disclosure channel for this project yet. A private
reporting channel and a response process will be published here before any
release intended for use, and this file will be updated at that time.

Do **not** send reports about CloudNativePatroni to the CloudNativePG security
contacts. They are the maintainers of a different project, they cannot act on
issues that exist only in this fork, and they have not agreed to receive them.

If you have found a vulnerability that affects **CloudNativePG itself** — that
is, code inherited unchanged from upstream, which at this milestone is nearly
all of it — report it to the CloudNativePG project through the process that
project publishes, so that its users benefit from the fix. Please do not open a
public issue in either repository for an unfixed vulnerability.

## Scope note

Because this fork is at milestone M0, the security-relevant behaviour of the
inherited code is unchanged from its derivation point: CloudNativePG at commit
`b226821` (2026-08-06), which is post-1.30.0 development on the upstream `main`
branch and therefore contains changes that the 1.30.0 release does not. Do not
map this repository onto the 1.30.0 advisory surface. The high-availability
architecture described in the README is a target, not an implementation, and
none of its safety properties are claimed to hold today. The limits that will
apply even once it is implemented are stated in the README under "What is not
claimed".

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.
