# Security Policy

## Reporting a vulnerability
Please report suspected vulnerabilities privately to the project maintainers. Do not open a public issue. Include reproduction steps and the affected component. We aim to acknowledge reports within 3 business days.

## Scope and design
The security architecture, threat model and controls are documented in [docs/SECURITY_ARCHITECTURE.md](docs/SECURITY_ARCHITECTURE.md). Open security-relevant gaps are tracked in [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md).

## Handling secrets
- Secrets are supplied via environment or a secret manager. `.env` is git-ignored.
- Production startup fails if required secrets are missing or weak.
- If a secret is exposed, rotate it immediately. See [docs/OPERATIONS_RUNBOOK.md](docs/OPERATIONS_RUNBOOK.md).
