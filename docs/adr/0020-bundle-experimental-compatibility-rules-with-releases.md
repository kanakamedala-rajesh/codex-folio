# Bundle experimental compatibility rules with releases

Exact Continuation and Shared Work Home authentication switching rely on narrow version-sensitive adapters. They will run only when the installed Codex version and platform match a compatibility manifest bundled and signed with the CodexFolio release. Remote configuration cannot silently expand that allowlist. Supporting a changed Codex version therefore requires reviewed adapter tests and a new CodexFolio release, while ordinary isolated-profile launching and analytics remain independent wherever their documented interfaces still work.
