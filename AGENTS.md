# Project Agent Instructions

This file provides guidance to AI agents (Codewhale, Claude Code, etc.) when working with code in this repository.

## File Location

Save this file as `AGENTS.md` in your project root so the CLI can load it automatically.

## Build and Development Commands

```bash
# Build
# cargo build              # Rust projects
# npm run build            # Node.js projects
# python -m build          # Python projects

# Test
# cargo test               # Rust
# npm test                 # Node.js
# pytest                   # Python

# Lint and Format
# cargo fmt && cargo clippy  # Rust
# npm run lint               # Node.js
# ruff check .               # Python
```

## Architecture Overview

<!-- Describe your project's high-level architecture here -->
<!-- Focus on the "big picture" that requires reading multiple files to understand -->

### Key Components

<!-- List and describe the main components/modules -->

### Data Flow

<!-- Describe how data flows through the system -->

## Configuration Files

<!-- List important configuration files and their purposes -->

## Extension Points

<!-- Describe how to extend the codebase (add new features, tools, etc.) -->

## GitHub Operations

Use the `gh` CLI for **all** GitHub operations — issues, pull requests, reviews,
releases, workflow runs, repository settings, and API queries. It is already
installed and authenticated in this environment (`gh auth status` shows an active
account), so do not ask the user for a token or re-run `gh auth login`.

- Do not use the GitHub web UI, raw `curl` calls against the REST/GraphQL API,
  or other GitHub clients for work `gh` can do.
- Prefer the specific subcommand over escape hatches: `gh issue`, `gh pr`,
  `gh release`, `gh run`, `gh repo`, `gh api` (only for what has no subcommand).
- Confirm the target repository with `gh repo view` before creating or closing
  anything, and never push, publish, or change repository settings without the
  user asking for it.
- Treat `gh` output as ground truth: read the command output and report what it
  says rather than assuming a mutation succeeded.

## Commit Messages

Use conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
