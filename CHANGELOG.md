## 0.3.4 - 2026-01-30

- Fixed critical service selector mismatch causing "No available server" errors with Traefik and other ingress controllers
- Service selectors now use `GetWordpressLabelsForMatching()` and `GetSFTPLabelsForMatching()` to match deployment pod labels
- Removed redundant `DisableTLS` field from IngressConfig, simplified TLS configuration
- Bumped chart/app version to `0.3.4`.

## 0.3.2 - 2026-01-30

- Added phpMyAdmin ingress configuration and bumped to `0.3.2`.
- Updated CRD schema and chart features (ingress annotations, ingressClassName, phpMyAdmin toggle).
- Miscellaneous chart improvements.

(See commits for details.)


## 0.3.3 - 2026-01-30

- phpMyAdmin: operator now deletes global Ingress when a `WordPressSite` sets `spec.disablePhpMyAdmin: true` and recreates it when disabled flag is removed.
- Bumped chart/app version to `0.3.3`.
