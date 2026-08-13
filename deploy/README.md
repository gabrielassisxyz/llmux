# llmux deployment

## Environment configuration

The binary requires configuration via environment variables.

- An environment-file template `llmux.env.template` is provided in this directory. It contains names and placeholders only.
- **Do not put proxy or account keys in command-line flags, checked-in unit files, shell history, or example files.**
- Place the real environment file outside the repository, e.g., in `~/.config/llmux/llmux.env`.
- Ensure the environment file is owner-readable only (mode `0600`).
- The database directory should be owned by the user and should not grant write access to other users.
- Startup messages may name a missing variable but will never print its value.
- Diagnostic commands in the runbook must not dump the process environment.
- Key changes require restart. Restart is the only key-reload mechanism.

## Service definition

A reference user-service definition `llmux.service` is provided for systemd:

- It runs the static binary as the current user.
- It references an owner-readable environment file outside the repository.
- It restarts only on process failure.
- It disables core dumps (`LimitCORE=0`) and sets an owner-only umask (`UMask=0077`) to prevent leaking credentials or prompt text.
- It sets `GOMEMLIMIT` above the aggregate memory budget with room to spare.

The binary does not depend on systemd or any particular supervisor; the unit is a reference, not a requirement.
