# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Argus, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, use one of these methods:

1. **GitHub Security Advisory**: Go to the [Security tab](https://github.com/BeLazy167/argus/security/advisories) and create a new advisory
2. **Email**: Contact the maintainers directly (see GitHub profile)

## What Qualifies

- Authentication or authorization bypass
- Injection vulnerabilities (SQL, command, template)
- Secrets or credentials exposed in code or logs
- Prompt injection that bypasses sanitization
- Path traversal in file handling
- Denial of service via resource exhaustion

## Response Timeline

- **48 hours**: Acknowledgment of your report
- **7 days**: Initial assessment and severity classification
- **30 days**: Fix deployed (for critical/high severity)

## Supported Versions

Only the latest version on `main` is supported with security updates.
