# Security Policy

Bloom manages accounts on your media server and stores who watched what. That
makes security part of the product, not an afterthought. This page explains
which versions get fixes and how to report a problem privately.

## Supported Versions

Bloom is pre-release. Until the first tagged release, only the latest commit on
`main` receives security fixes. Once releases begin, this table will list them.

| Version | Supported |
|---|---|
| `main` (pre-release) | yes |

## Reporting a Vulnerability

**Please do not open a public issue, pull request, or discussion for an
unpatched vulnerability.** Public disclosure before a fix exists puts every
Bloom user at risk.

Report privately through GitHub's private advisory form:
https://github.com/BonzTM/bloom/security/advisories/new

Include what you can of the following. Partial reports are still welcome.

- The affected version, commit, or image tag.
- What the problem lets an attacker do.
- Steps to reproduce, a proof of concept, or a failing test.
- Any workaround you know of.
- How you would like to be credited, or that you prefer to stay anonymous.

## What Happens Next

- **Acknowledgement:** within 3 business days.
- **Triage:** a severity assessment within 7 business days, with updates at
  least weekly until it is resolved.
- **Coordinated disclosure:** please keep the report private until a fix ships
  or 90 days have passed, whichever comes first. We will agree a disclosure date
  with you and publish a GitHub Security Advisory when the fix is released.
- **Credit:** with your consent, reporters are credited in the advisory and the
  release notes. There is no paid bounty program.

## Scope Notes

Bloom talks to a media server with an administrator credential. A vulnerability
in Bloom can therefore reach the media server. Reports about how Bloom stores,
uses, or exposes those credentials are in scope and treated as high severity.

Thank you for helping keep Bloom and its users safe.
