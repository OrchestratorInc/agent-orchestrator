# Harness detail pages

Captured from the real development Electron app on macOS using isolated AO data, with the implementation in `8c5d85923`. The screenshots show actual installed harness readiness and model catalogs.

- [Account (light)](account-light.png): installation, authentication, and maintenance actions.
- [Models (light)](models-light.png): the harness model catalog.
- [Health (dark)](health-dark.png): resolved executable and diagnostic information.
- [Logout confirmation](logout-confirm.png): native logout confirmation naming the host.

Native review covered list-to-detail navigation, the three tabs, both themes, login terminal open/close, and logout/uninstall confirmation cancellation. Install/update/uninstall/logout commands were not executed against the machine's installed harnesses or credentials; command selection and lifecycle behavior were exercised with backend and UI tests.
