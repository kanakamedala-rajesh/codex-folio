# Ship an offline embedded dashboard

Every production dashboard asset, dependency, and font will be pinned, versioned, built ahead of time, and embedded into the Go executable. The browser loads no CDN or runtime package resources, and the loopback service enforces a restrictive Content Security Policy. Only explicitly enabled update checks and remote telemetry may contact CodexFolio-operated endpoints. This makes the dashboard usable offline, keeps the binary/archive boundary complete, and prevents remote frontend dependencies from expanding the local trust boundary.
