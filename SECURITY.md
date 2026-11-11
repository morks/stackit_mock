# Security

Der Mock ist für lokale Entwicklung und Tests gedacht und **nicht** für den Betrieb in einem ungeschützten Netz: Er akzeptiert (standardmäßig) jede Authentifizierung, bietet keine TLS-Terminierung und seine Admin-API (`/_admin/*`) ist unauthentifiziert. Nur an Loopback oder in einem abgeschotteten Lab-Netz betreiben.

Schwachstellen bitte über „Report a vulnerability“ im Security-Tab des Repositories melden (Private Vulnerability Reporting), nicht als öffentliches Issue.

Automatische Prüfungen (GitHub Actions): `govulncheck` (Abhängigkeiten + Standardbibliothek), `gosec`, CodeQL, Dependency Review bei PRs, Dependabot; Läufe bei jedem Push/PR und wöchentlich.
