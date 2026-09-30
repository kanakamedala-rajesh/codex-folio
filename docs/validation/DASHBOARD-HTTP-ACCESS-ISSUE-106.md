# Dashboard access without certificate setup — #106 follow-up

## Approved scope

The user reported Chrome's certificate-authority error at the printed HTTPS
address after the earlier analytics timeout repair. They approved replacing
persistent browser trust with a fresh per-launch loopback HTTP URL while
retaining pinned TLS for private CLI commands. ADR 0038 records the successor
contract; the earlier timeout report remains historical evidence.

## Implementation

- Production composition uses separate loopback listeners: ephemeral HTTP for
  the browser and certificate-pinned TLS for private command requests.
- Ordinary startup prints a single-use fragment URL. The browser removes the
  fragment before exchanging it; no certificate installation or TLS exception
  is required. Opening another fresh link in the same tab also works.
- HTTP sessions use an explicit header and tab session storage, with CSRF
  retained. No authentication cookies, durable browser trust, or renewal across
  owner restarts. Sessions expire and cannot authorize a replacement owner.
- Private command routes are unavailable on the HTTP listener. Browser routes
  are unavailable on the production TLS command listener. Host, Origin, replay,
  CSP and cross-origin controls remain in place.

## Chrome DevTools MCP observation

Observed on WSL2 kernel `6.6.87.2-microsoft-standard-WSL2`, using the actual
Chrome DevTools MCP browser, reported Chrome `153.0.0.0` on Linux. An isolated
production-composition fixture used the normal service factory, pinned CLI
unlock and dashboard-link commands, synthetic in-memory vault protection, and
an empty temporary database. It did not access real profiles or credentials.
The temporary manual test harness was removed after execution.

- `new_page` opened the fresh HTTP fragment URL in a clean isolated browser
  context without importing a certificate or setting a certificate bypass.
- The dashboard rendered “Choose your first Identity Profile” from the empty
  fixture; authenticated metadata returned HTTP 200.
- The URL fragment was gone, a tab session existed, and no browser cookies or
  persistent trust entry existed.
- Reloading the same tab retained HTTP 200 authorization.
- A separate clean browser context opening the plain address displayed
  “Relaunch CodexFolio”; metadata returned HTTP 401 with no stored session.
- Narrow viewport inspection rendered the first-profile state and navigation
  without a certificate interstitial. A screenshot was inspected locally.

The user's already-running older service was left untouched. The built binary
was rebuilt; its attempted isolated startup correctly refused a second
OS-user owner. MCP therefore exercised the production service composition via
isolated test paths, not the user's real state. An existing old process must
be restarted to run the new binary; an old HTTPS bookmark remains obsolete.

## Automated evidence

The dedicated `http-dashboard` real Chromium suite passed all seven checks:
fresh opening without certificate overrides, fragment removal and profile
loading; header sessions without cookies/trust; same-tab refresh; bootstrap
replay refusal; other-port isolation; plain-address refusal in a clean browser;
and a second fresh link in the same tab. Existing HTTPS fixtures retain their
explicit test pins and legacy trust regression coverage.

Additional verification results and reviewed commit identity are recorded in
the commit delivery report. Native/live platform qualification and release
eligibility remain separate; HTTP does not prove the identity of an arbitrary
local process to the browser.
