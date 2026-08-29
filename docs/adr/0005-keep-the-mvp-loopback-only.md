# Keep the MVP loopback-only

The MVP dashboard will bind only to loopback and will not expose LAN or remote access. The application will nevertheless keep presentation transport and authorization concerns separate from domain services so a future, explicitly secured multi-device mode can be designed without making the current local server remotely reachable.
