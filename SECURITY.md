# Security policy

Report a vulnerability privately through GitHub's private vulnerability reporting (the repository's Security tab, "Report a vulnerability"), not in a public issue. Include the mimicry version or commit, the profile, and a minimal reproduction.

Fixes go to the latest release (to `main` before the first release).

## How the repo is checked

- **Secret scanning** with push protection: a push carrying a known kind of credential is blocked.
- **Dependabot** alerts and security updates for the Go modules (the library, `internal/harness`, `internal/bench`): a dependency with a published advisory gets an alert and a pull request moving it to the fixed version. Routine version updates cover only the GitHub Actions the workflows use (`.github/dependabot.yml`); the Go modules are pinned on purpose.
- **CodeQL** (`.github/workflows/codeql.yml`) analyses the Go code and the workflows on every pull request, every push to `main` and weekly.
- **govulncheck** (the `vuln` job in `.github/workflows/ci.yml`) checks each module against the Go vulnerability database; a vulnerability the code can reach fails CI.
