## 0.3.2 - 2026-01-30

- Added phpMyAdmin ingress configuration and bumped to `0.3.2`.
- Updated CRD schema and chart features (ingress annotations, ingressClassName, phpMyAdmin toggle).
- Miscellaneous chart improvements.

(See commits for details.)


## 0.3.3 - 2026-01-30

- phpMyAdmin: operator now deletes global Ingress when a `WordPressSite` sets `spec.disablePhpMyAdmin: true` and recreates it when disabled flag is removed.
- Bumped chart/app version to `0.3.3`.
