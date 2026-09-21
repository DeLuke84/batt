# Plan: One-Time-Charge für Upstream-PR härten

> Arbeitsbranch: `feat/one-time-charge`
>
> Vergleichsbasis: `upstream/master`
>
> Zweck: Review-Findings schließen, macOS-Nachweise ausführen und erst danach den Pull Request zu `charlie0129/batt` erstellen.

## Ausgangslage

Der Branch implementiert einen persistenten One-Time-Charge vertikal über Config, Daemon, Client, CLI, Statusausgabe und macOS-Menü. Er liegt 12 Commits vor `upstream/master`; der Working Tree war vor diesem Plan sauber. Die Implementierung deckt Legacy- und Firmware-Charge-Control ab und besitzt umfangreiche Unit-Tests. Auf dem Linux-Agentenserver konnten diese nicht ausgeführt werden: Go fehlt dort und die eingecheckten Hilfsprogramme sind macOS-arm64-Binaries.

## Zielzustand

- Persistierter und In-Memory-Zustand widersprechen sich auch bei Schreibfehlern nicht.
- CLI und Menü bieten keine Aktion an, die der Daemon im bekannten Zustand sofort ablehnt.
- Statusmeldungen unterscheiden „Adapter nicht unterstützt“ von „Adapter deaktiviert“.
- Firmware-Schwelle (`target - 1`) wird in Daemon und Menü identisch beurteilt.
- Neue CLI- und Client-Pfade besitzen fokussierte Tests.
- `go test ./...`, Lint und Build laufen auf dem Mac grün.
- Erst dann entsteht der Upstream-PR.

## Umsetzung

### 1. Branch auf dem Mac übernehmen

```bash
git fetch origin upstream
git switch feat/one-time-charge
git pull --ff-only origin feat/one-time-charge
git status --short --branch
```

Vor Änderungen prüfen, dass keine fremden lokalen Änderungen vorliegen.

### 2. Automatischen Abschluss transaktional machen

Dateien:

- `pkg/daemon/chargeonce.go`
- `pkg/daemon/chargeonce_test.go`

In `completeChargeOnce` darf ein fehlgeschlagenes `Save()` den Vorgang nicht als abgeschlossen melden. Bei Fehler:

1. das gelöschte Target im Speicher wiederherstellen,
2. `false` zurückgeben, damit der nächste Loop erneut versucht,
3. kein Completion-Event und keinen Erfolgslog veröffentlichen.

Regressionstest mit `saveErr`: Target bleibt gesetzt und der Abschluss wird nicht bestätigt. Danach erfolgreicher Retry.

### 3. GUI-Zulassung an die Daemon-Regeln angleichen

Dateien:

- `pkg/gui/controller.go`
- `pkg/gui/controller_test.go`

Zwei Abweichungen schließen:

1. Bei vorhandener Adapter-Control muss ein bekannter, deaktivierter Adapter beide Start-Aktionen deaktivieren. `updateAdapterState` muss danach auch `updateChargeOnceControls` aktualisieren.
2. Im Firmware-Modus gilt ein Ziel unter 100 % bei `currentCharge >= target - 1` bereits als erreicht. „Charge to Limit Now“ muss dann ebenso deaktiviert sein wie die Daemon-Route.

Die Eligibility-Helfer sollen die benötigten Fakten explizit erhalten: Charge-Control-Modus sowie Adapter-Capability/-Bekanntheit/-Status. Testmatrix mindestens für Legacy/Firmware, Adapter unterstützt/nicht unterstützt/ausgeschaltet und 79/80-%-Grenze.

### 4. Statusmeldung bei fehlender Adapter-Control korrigieren

Dateien:

- `cmd/batt/status.go`
- `cmd/batt/status_test.go`

„adapter is disabled“ nur ausgeben, wenn `capabilities.AdapterControl` wahr ist und der gelesene Adapterstatus deaktiviert meldet. Ohne Adapter-Control darf der Defaultwert `false` nicht als Hardwarezustand interpretiert werden. Das gilt mindestens für die neue One-Time-Charge-Narration; den bestehenden identischen Pfad unterhalb des Hysterese-Limits im selben Zug konsistent korrigieren.

### 5. CLI- und Client-Verkabelung testen

Fokussierte Tests ergänzen für:

- Registrierung von `batt charge now`, `full`, `cancel`,
- `cobra.NoArgs`,
- Zuordnung der drei Befehle zu den richtigen Client-Aufrufen,
- HTTP-Methode und Pfad der neuen Client-Methoden.

Vorhandene Test-Seams und lokale Testmuster verwenden; keine neue Testarchitektur nur für dieses Feature einführen.

### 6. macOS-Verifikation

Auf dem Mac ausführen:

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test ./...
make lint
make
```

Zusätzlich manuell mit laufendem Daemon prüfen:

1. `batt charge now` innerhalb der Hysterese startet sofort.
2. Ziel erreicht → konfigurierte Bandbreite gilt wieder.
3. `batt charge full` lädt einmalig auf 100 %.
4. Cancel stellt die normale Regel sofort wieder her.
5. Force Discharge und Kalibrierung kollidieren nicht mit aktivem One-Time-Charge.
6. Menüstatus folgt Start, Cancel und Abschluss nach erneutem Öffnen.
7. Firmware-Mac bei `target - 1` zeigt keine ausführbare Startaktion.

Hinweis: Die bestehenden GitHub-Workflows bauen und linten, führen `go test ./...` aber derzeit nicht aus. Der lokale Testnachweis ist deshalb verpflichtend. Eine CI-Teststufe ist sinnvoll, aber als separater Maintenance-Commit oder eigener PR zu behandeln, falls sie den Feature-PR unnötig verbreitert.

### 7. PR vorbereiten

- `git diff --check upstream/master...HEAD`
- Commits auf klare, semantische Subjects prüfen; kein History-Rewrite ohne bewusste Entscheidung.
- Diese Plan-Datei vor dem PR aus dem finalen Diff entfernen, sofern sie nicht ausdrücklich Upstream-Dokumentation werden soll.
- Branch zu `origin` pushen.
- PR gegen `charlie0129/batt:master` mit Benutzerwirkung, Firmware-Kompromiss und Testnachweisen beschreiben.
- Upstream-CI vollständig abwarten; Findings vor Review-Anfrage schließen.

## Review-Anker

- Persistenzfehler: `pkg/daemon/chargeonce.go`, `completeChargeOnce`
- Adapter-/Firmware-Drift: `pkg/gui/controller.go`, `updateChargeOnceControls`, `canChargeOnceToLimit`
- Falsche Adaptermeldung: `cmd/batt/status.go`, `chargingNarration`
- Kernlogik: `pkg/daemon/chargeonce.go`
- Große Testsuite: `pkg/daemon/chargeonce_test.go`
- Workflow-Lücke: `.github/workflows/gochecks.yml`, `.github/workflows/build-test-binary.yml`

## Offene Entscheidung

Soll `go test ./...` bereits im Feature-PR in `gochecks.yml` ergänzt werden oder als separater kleiner CI-PR folgen? Default-Empfehlung: erst lokal grün nachweisen; CI-Änderung separat halten, sofern Upstream keine Tests im Feature-PR verlangt.
