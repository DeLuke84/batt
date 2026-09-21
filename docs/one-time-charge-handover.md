# Handover: BATT One-Time-Charge

## Worum es geht

Der Branch `feat/one-time-charge` im Fork `DeLuke84/batt` soll nach Review-Härtung als Pull Request an `charlie0129/batt` gehen.

## Wo wir stehen

- Branch ist sauber und zu `origin` gepusht.
- Noch kein Upstream-PR erstellt.
- Der vollständige Plan liegt in `docs/one-time-charge-pr-plan.md`.
- Die Implementierung umfasst Config, Daemon, Client, CLI, Statusausgabe und macOS-Menü.
- Der Review fand Persistenz- und GUI-Inkonsistenzen sowie fehlende CLI-/Client-Tests.
- Auf dem Linux-Server wurden keine Tests ausgeführt; Go fehlt und das Projekt ist macOS-spezifisch.

## Nächste Schritte

1. `docs/one-time-charge-pr-plan.md` vollständig lesen.
2. `completeChargeOnce` bei `Save()`-Fehler transaktional reparieren und testen.
3. GUI-Zulassung an Adapterstatus und Firmware-`target-1`-Schwelle angleichen.
4. Falsche Adaptermeldung in `chargingNarration` korrigieren.
5. CLI- und Client-Verkabelung fokussiert testen.
6. Auf dem Mac ausführen:

   ```bash
   go mod tidy
   git diff --exit-code -- go.mod go.sum
   go test ./...
   make lint
   make
   ```

7. Plan und Handover vor dem Upstream-PR aus dem finalen Diff entfernen, sofern sie nicht bewusst als Upstream-Dokumentation bleiben sollen.
8. Erst nach grünen Nachweisen den PR gegen `charlie0129/batt:master` erstellen.

## Offene Entscheidung

Soll `go test ./...` im Feature-PR in GitHub Actions aufgenommen oder als separater CI-PR nachgezogen werden? Default: lokal zwingend testen und die CI-Änderung separat halten.

## Datei-Anker

- `docs/one-time-charge-pr-plan.md` — vollständiger Umsetzungsplan
- `pkg/daemon/chargeonce.go` — Kernlogik und Persistenzfehler
- `pkg/daemon/chargeonce_test.go` — Daemon-Regressionstests
- `pkg/gui/controller.go` — Adapter-/Firmware-Drift im Menü
- `pkg/gui/controller_test.go` — GUI-Testmatrix
- `cmd/batt/status.go` — falsche Adaptermeldung
- `.github/workflows/gochecks.yml` — führt derzeit kein `go test ./...` aus
