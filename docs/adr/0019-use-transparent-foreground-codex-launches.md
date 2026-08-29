# Use transparent foreground Codex launches

The deterministic CLI contract will run the user's installed Codex executable in the foreground with the selected Identity Home, pass through remaining arguments and terminal streams, forward signals according to platform conventions, and return Codex's exit status. CodexFolio records only Managed Launch metadata and does not introduce detached Codex execution in the MVP. Authentication remains an invocation of Codex's own login flow inside that Identity Home.
