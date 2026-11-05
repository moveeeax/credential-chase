# credential-chase

Per-assignment credential checklist for small travel-nurse staffing agencies:
work out which documents a facility will actually demand, which ones are
missing or about to lapse, and who has to be chased today.

This repository is the engine, not the product. `credchase` is a CLI that
reads YAML and prints text, JSON or CSV. It **sends nothing** — no SMS, no
email, no fax. Delivery and the magic-link upload page are separate layers
that need real credentials; see [What is not here](#what-is-not-here).

## Why it exists

A start date slips because a BLS card expired, or because nobody noticed the
facility wanted the card to stay valid for thirty days *past* the start.
Spreadsheets miss the second case entirely: the card is valid today, so the
cell is green.

`credchase` measures coverage from the start date, not from today.

## What it does

- **Facility requirement matrix** — one versioned YAML file per facility,
  hand-entered from the packet the facility sends. A schema linter with
  stable rule codes (`CM001`…`CM015`) catches the typos before they cost a
  placement, including unknown keys.
- **Missing/expiring engine** — evaluates every placement against its
  facility matrix. Statuses: `missing`, `expired`, `short` (valid, but not
  for long enough), `waived`, `ok`. Severities follow a warn/critical model:
  an open item is a warning while there is runway, and critical at or inside
  seven days.
- **Coordinator board** — one screen, red/amber/green per placement, sorted
  by start date.
- **Escalation schedule** — fixed windows at T-21, T-14, T-7, T-3 and T-1,
  SMS and email to the nurse plus one daily digest to the coordinator. A
  ledger makes it idempotent: rerunning the same day chases nobody twice.
- **Audit export** — the ledger as CSV, one row per message, with the day it
  went out.

## Install

Requires Go 1.26 or newer.

```sh
go install github.com/moveeeax/credential-chase/cmd/credchase@latest
```

Or from a clone:

```sh
git clone https://github.com/moveeeax/credential-chase.git
cd credential-chase
go build -o bin/credchase ./cmd/credchase
```

## Usage

The repository ships two facility matrices and four placements under
`examples/`. They are pinned to fictional 2026 dates, so pass `-as-of` to
evaluate them at the moment they were written for. Every command below runs
against a fresh clone, from the repository root.

### 1. Lint the requirement matrices

```sh
./bin/credchase lint -as-of 2026-08-01 examples/facilities
```

```
2 matrix file(s) checked: 0 error(s), 0 warning(s)
```

Try it on the deliberately broken fixture to see the rule codes:

```sh
./bin/credchase lint -as-of 2026-08-01 internal/matrix/testdata/broken.yaml
```

### 2. Look at the board

```sh
./bin/credchase board -as-of 2026-08-01
```

```
coordinator board — as of 2026-08-01

STATUS  START      PLACEMENT    NURSE                    T-     FACILITY               OPEN
RED     2026-08-05 PL-1042      Dana Whitfield           4      St Mary's Regional     acls-aha,bls-aha,facility-orientation-packet
RED     2026-08-08 PL-1045      Alicia Boone             7      Cedar Ridge Medical C… cna-certification,dementia-care-training,hipaa-attestation
AMBER   2026-08-19 PL-1043      Marcus Ellison           18     St Mary's Regional     n95-fit-test
GREEN   2026-09-14 PL-1044      Priya Raman              44     St Mary's Regional

4 placement(s): 2 red, 1 amber, 1 green
```

The board exits `2` while any placement is red, so it drops straight into
cron or CI.

### 3. See why a placement is red

```sh
./bin/credchase check -placements examples/placements/pl-1042-whitfield.yaml -as-of 2026-08-01
```

```
RED  PL-1042 — Dana Whitfield at St Mary's Regional
  starts 2026-08-05 (T-4), 3 critical / 0 warn / 6 ok
  critical acls-aha               missing  nothing on file
  critical bls-aha                short    expires 2026-08-31, 26 days past start, facility wants 30
  critical facility-orientation-packet missing  nothing on file
  ok       flu-vaccination        waived   waived by facility: Off-season start; facility waiver ref SMR-W-3391, 2026-07-18
  ok       hipaa-attestation      ok       on file since 2026-07-01
  ok       n95-fit-test           ok       valid to 2027-07-20, 349 days past start
  ok       photo-id               ok       on file since 2026-07-01
  ok       rn-license-mn          ok       valid to 2027-04-30, 268 days past start
  ok       tb-screening           ok       valid to 2027-06-15, 314 days past start
```

`bls-aha` is the case that matters: the card is valid on 1 August and stays
valid past the start date, but only for 26 of the 30 days St Mary's requires.

Add `-json` to any of `check`, `board` or `lint` for machine-readable output.

### 4. Work out today's chase

```sh
./bin/credchase chase -as-of 2026-08-01 -ledger ./chase.ledger.json
```

Each action prints the channel, the recipient, the escalation window and the
exact message body — ready to hand to a delivery layer. Nothing is sent.
Once the messages really have gone out, record them:

```sh
./bin/credchase chase -as-of 2026-08-01 -ledger ./chase.ledger.json -commit
./bin/credchase chase -as-of 2026-08-01 -ledger ./chase.ledger.json
```

```
chase plan — as of 2026-08-01
nothing due

0 action(s) due, 7 suppressed as already sent
```

Rerun it on 6 August and PL-1043 is chased again, because it has crossed
T-14. The dedup key is placement + window + channel, so each escalation
fires exactly once.

### 5. Export the audit trail

```sh
./bin/credchase audit -ledger ./chase.ledger.json
```

```
sent_on,placement_id,nurse,facility,channel,recipient,window_days,items,subject,key
2026-08-01,PL-1042,Dana Whitfield,St Mary's Regional,email,dana.whitfield@example.com,7,acls-aha bls-aha facility-orientation-packet,...
```

## Data model

A facility matrix (`examples/facilities/*.yaml`):

```yaml
schema_version: 1
facility:
  id: st-marys-rochester
  name: St Mary's Regional
  intake:
    method: fax          # fax | email
    fax: "+1-555-0100"
    cover_sheet_required: true
updated: 2026-07-01
source: "Requirement packet, emailed by facility credentialing 2026-06-28"
requirements:
  - code: bls-aha
    name: BLS provider card (AHA)
    category: certification   # license|certification|health|training|form|identity
    expires: true
    lead_days: 30             # must stay valid 30 days past the start date
    accepts: [pdf, jpg]
  - code: acls-aha
    name: ACLS provider card (AHA)
    category: certification
    expires: true
    lead_days: 30
    applies_to:
      disciplines: [RN]
      units: [ICU, ED]        # only these placements need it
```

A placement (`examples/placements/*.yaml`):

```yaml
schema_version: 1
id: PL-1042
nurse:
  name: Dana Whitfield
  discipline: RN
  phone: "+1-555-0143"
  email: dana.whitfield@example.com
facility: st-marys-rochester
unit: ICU
start_date: 2026-08-05
coordinator:
  name: Rosa Delgado
  email: rosa@example-staffing.com
documents:
  - requirement: bls-aha
    received: 2026-05-02
    expires: 2026-08-31
    file: whitfield-bls.pdf
  - requirement: flu-vaccination
    waived: true
    waiver_note: "Off-season start; facility waiver ref SMR-W-3391, 2026-07-18"
```

Both files are decoded strictly: an unknown key is an error, not a silently
ignored line. A misspelled `requirments:` producing an empty checklist is
exactly the failure this tool exists to prevent.

Documents filed against a requirement code the facility does not use are
reported as **orphans** rather than credited to anything.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | clean |
| `1` | usage or input error (bad flag, unparseable file, unknown facility) |
| `2` | findings: lint errors, or at least one red placement |

`lint -strict` also fails on warnings.

## What is not here

Deliberately, for now:

- **Delivery.** No fax API, no Twilio, no SMTP. `chase` decides what should
  be sent and refuses to decide it twice; wiring it to a provider needs paid
  accounts and is the next slice.
- **The nurse upload page.** Magic-link tokens and encrypted object storage.
- **Persistence.** YAML files and a JSON ledger, not Postgres.
- **Primary source verification.** Nursys does licence verification for
  free. Never compete with it.
- **Anything in the explicit v1 exclusion list**: ATS/VMS integration, a
  public API, OCR, background checks, payroll, scheduling, a nurse app, or a
  general facility-requirement database.

## Development

```sh
go build ./...
go vet ./...
go test -race ./...
```

Tests run against the same `examples/` fixtures the README uses, so a change
that breaks the demo breaks the build. CI additionally executes the usage
example above on every push.

## Privacy

Placement records and chase ledgers contain nurse names, phone numbers and
email addresses. Everything in `examples/` and `testdata/` is fictional and
uses reserved `555-01xx` numbers and `example.com` domains. Real records
belong nowhere near this repository — see `.gitignore`.

## Licence

MIT. See [LICENSE](LICENSE).
