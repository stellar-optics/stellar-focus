# Security Policy

## Supported versions

This project is pre-1.0. Security fixes are applied to the latest release and
to `main`; older tags are not patched.

| Version | Supported |
| --- | --- |
| 0.1.x | ✅ |
| < 0.1 | ❌ |

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Report it through
[GitHub's private vulnerability reporting](https://github.com/stellar-optics/stellar-focus/security/advisories/new),
which opens a private channel with the maintainers.

Please include:

- what the issue is and why it matters;
- the XDR payload or fixture that triggers it — the assertion library runs
  offline and deterministically, so a payload and a short test are usually a
  complete reproduction;
- the version or commit you tested;
- any suggested fix, if you have one.

### What to expect

- **Acknowledgement** within 5 working days.
- **An assessment** within 10 working days.
- **A fix and advisory** for confirmed issues, coordinated with you on timing.

You will be credited unless you prefer otherwise.

## Threat model

stellar-focus has two quite different halves, and they carry different risk.

**The assertion library** (`pkg/focus`, `pkg/focus/require`, `pkg/golden`)
runs inside other people's test suites. It parses untrusted XDR, touches no
network, executes nothing it decodes, and reads only the fixture files it is
pointed at.

**The capture command** (`pkg/capture`, `cmd/focus`) talks to an RPC endpoint
the user chooses and writes files the user names.

### In scope

- **A panic escaping an assertion.** Assertions run behind a recover
  specifically because generated XDR accessors dereference absent union arms.
  A panic that escapes destroys the output of every other test in a package,
  so it is treated as a security-relevant defect, not a cosmetic one.
- **An assertion passing when it should fail.** A test helper that silently
  approves the wrong value is worse than no helper: it converts a real defect
  into a green build. This includes an ignore rule suppressing more than it
  says it does.
- Unbounded memory or CPU consumption triggered by a malformed payload or a
  hostile fixture file.
- `focus capture` or `focus verify` reading or writing a path the user did not
  name, or following a path out of the target directory.
- Any network connection made by the assertion library or by `pkg/golden`.
  Those must remain offline; a test helper that phones home would be a serious
  defect.
- A credential leaked into output. `--rpc-url` may contain an API key, so it is
  never echoed.

### Out of scope

- Vulnerabilities in `github.com/stellar/go-stellar-sdk` or in
  `stellar-xdr-lens`. Report those to their own projects; we will pick up the
  fixed version.
- A malicious RPC endpoint returning plausible but false data during capture.
  focus records what the endpoint returns; point it at an endpoint you trust,
  and use `focus verify` against a second one if that matters to you.
- A hand-edited fixture file that no longer matches the chain. That is what
  `focus verify` is for, and it reports rather than prevents.
- Anything requiring an attacker to already control the machine running the
  tests.

## For users

- **`--rpc-url` may contain a secret.** Many hosted providers embed an API key
  in the URL. Prefer an environment variable over a shell command that lands
  in your history, and check before pasting a command line into an issue.
- **Captured fixtures may contain public but identifying data** — account
  addresses, amounts, memos. They come from a public ledger, but think before
  committing a fixture captured from a private or pre-launch network.
- **focus never signs or submits anything.** It reads chain data and writes
  files; it has no key handling of any kind.
