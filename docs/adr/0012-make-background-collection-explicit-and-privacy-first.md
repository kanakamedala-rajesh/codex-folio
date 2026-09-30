# Make background collection explicit and privacy-first

**Partially superseded by [ADR 0037](0037-separate-companion-lifetime-from-consent-and-attribution.md)**: background collection consent is separate from automatic companion lifetime and OS-login enrollment. Explicit passphrase mode retains its locked-session restrictions; qualified storage follows [ADR 0035](0035-qualify-prompt-free-secure-storage.md). Original rationale follows.

On-demand collection remains the default. An explicitly installed service may collect on active-session events, refresh lightly while idle, and back off after provider or authentication failures. Native notifications use generic privacy-preserving text by default, especially on locked screens; detailed identity and quota content is an explicit per-device preference. Passphrase-backed headless services remain locked and collect nothing sensitive until the user unlocks the vault for that session.
