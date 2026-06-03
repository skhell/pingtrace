# Security Policy

## Supported Versions

| Version | Supported |
| ------- | --------- |
| 1.x     | Yes       |

## Reporting a Vulnerability

Please do not report security vulnerabilities through public GitHub issues.

Send a description of the issue to **tia@skhell.com** with the subject line
`[pingtrace] Security Vulnerability`. Include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept
- Your suggested fix if you have one

You will receive a response within 72 hours. If the issue is confirmed, a patched release
will be published as soon as possible and you will be credited in the changelog unless you
prefer to remain anonymous.

## Scope

pingtrace shells out to the OS `ping`, `traceroute`, and `tracert` binaries. It does not
open raw sockets, bind to any port, or run as a daemon. The attack surface is limited to:

- Parsing of target inputs (hostnames, IPs, CIDRs, CSV files)
- HTTP calls to ipinfo.io and PeeringDB when tokens are configured
- Reading and writing of config and export files under the user's home directory

Issues in third-party dependencies should be reported upstream. If a vulnerable dependency
directly affects pingtrace users, a new release pinning the fixed version will follow.
