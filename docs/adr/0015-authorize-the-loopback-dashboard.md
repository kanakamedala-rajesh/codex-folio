# Authorize the loopback dashboard

**Extended by [ADR 0036](0036-remember-explicitly-trusted-browsers.md)** for persistent browser trust and renewal of short-lived sessions. The original request-protection rationale is retained below.

Loopback binding limits remote exposure but does not establish caller identity. Each browser dashboard will therefore bootstrap through a one-time launcher secret, exchange it for a short-lived local session, and operate under strict Host, Origin, cross-origin, and CSRF controls. Credentials and vault material remain server-side. This preserves the convenience of a browser-hosted local UI without treating every local webpage or process as trusted.
