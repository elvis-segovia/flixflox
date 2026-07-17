# Security Policy

## Supported versions

Only the latest code on the `main` branch is supported with security fixes.

## Reporting a vulnerability

Please do **not** report security vulnerabilities through public GitHub issues.

Instead, report them privately via [GitHub Security Advisories](https://github.com/elvis-segovia/flixflox/security/advisories/new).

Please include:

- A description of the vulnerability and its impact
- Steps to reproduce or a proof of concept
- The affected endpoint(s) or component(s)

You can expect an initial response within a few days. Once the issue is confirmed and fixed, the fix will be released and the vulnerability disclosed responsibly.

## Scope notes

FlixFlox handles authentication (JWT), file uploads, and file serving — reports around path traversal, authentication/authorization bypass, token handling, and unsafe file processing are particularly relevant.
